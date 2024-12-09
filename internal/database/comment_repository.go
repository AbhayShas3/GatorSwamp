// internal/database/comment_repository.go
package database

import (
	"context"
	"fmt"
	"gator-swamp/internal/models"
	"gator-swamp/internal/utils"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// CommentDocument represents comment data in MongoDB
type CommentDocument struct {
	ID          string    `bson:"_id"`
	Content     string    `bson:"content"`
	AuthorID    string    `bson:"authorId"`
	PostID      string    `bson:"postId"`
	SubredditID string    `bson:"subredditId"`
	ParentID    *string   `bson:"parentId,omitempty"`
	Children    []string  `bson:"children"`
	CreatedAt   time.Time `bson:"createdAt"`
	UpdatedAt   time.Time `bson:"updatedAt"`
	IsDeleted   bool      `bson:"isDeleted"`
	Upvotes     int       `bson:"upvotes"`
	Downvotes   int       `bson:"downvotes"`
	Karma       int       `bson:"karma"`
}

// SaveComment creates or updates a comment in MongoDB
func (m *MongoDB) SaveComment(ctx context.Context, comment *models.Comment) error {
	doc := CommentDocument{
		ID:        comment.ID.String(),
		Content:   comment.Content,
		AuthorID:  comment.AuthorID.String(),
		PostID:    comment.PostID.String(),
		Children:  make([]string, len(comment.Children)),
		CreatedAt: comment.CreatedAt,
		UpdatedAt: comment.UpdatedAt,
		IsDeleted: comment.IsDeleted,
		Upvotes:   comment.Upvotes,
		Downvotes: comment.Downvotes,
		Karma:     comment.Karma,
	}

	// Convert Children UUIDs to strings
	for i, childID := range comment.Children {
		doc.Children[i] = childID.String()
	}

	// Handle optional ParentID
	if comment.ParentID != nil {
		parentIDStr := comment.ParentID.String()
		doc.ParentID = &parentIDStr
	}

	opts := options.Update().SetUpsert(true)
	filter := bson.M{"_id": doc.ID}
	update := bson.M{"$set": doc}

	_, err := m.Comments.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		return fmt.Errorf("failed to save comment: %v", err)
	}

	// If this is a new comment with a parent, update the parent's children array
	if comment.ParentID != nil {
		_, err = m.Comments.UpdateOne(
			ctx,
			bson.M{"_id": comment.ParentID.String()},
			bson.M{"$addToSet": bson.M{"children": comment.ID.String()}},
		)
		if err != nil {
			return fmt.Errorf("failed to update parent comment: %v", err)
		}
	}

	return nil
}

// GetComment retrieves a comment by ID
func (m *MongoDB) GetComment(ctx context.Context, id uuid.UUID) (*models.Comment, error) {
	var doc CommentDocument
	err := m.Comments.FindOne(ctx, bson.M{"_id": id.String()}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, utils.NewAppError(utils.ErrNotFound, "Comment not found", err)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get comment: %v", err)
	}

	return convertCommentDocumentToModel(&doc)
}

// GetPostComments retrieves all comments for a post
func (m *MongoDB) GetPostComments(ctx context.Context, postID uuid.UUID) ([]*models.Comment, error) {
	cursor, err := m.Comments.Find(ctx, bson.M{"postId": postID.String()})
	if err != nil {
		return nil, fmt.Errorf("failed to get post comments: %v", err)
	}
	defer cursor.Close(ctx)

	var comments []*models.Comment
	for cursor.Next(ctx) {
		var doc CommentDocument
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("failed to decode comment: %v", err)
		}

		comment, err := convertCommentDocumentToModel(&doc)
		if err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}

	return comments, nil
}

// UpdateCommentVotes updates the vote counts and karma for a comment
func (m *MongoDB) UpdateCommentVotes(ctx context.Context, commentID uuid.UUID, upvoteDelta, downvoteDelta int) error {
	filter := bson.M{"_id": commentID.String()}
	update := bson.M{
		"$inc": bson.M{
			"upvotes":   upvoteDelta,
			"downvotes": downvoteDelta,
			"karma":     upvoteDelta - downvoteDelta,
		},
		"$set": bson.M{
			"updatedAt": time.Now(),
		},
	}

	result, err := m.Comments.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("failed to update comment votes: %v", err)
	}

	if result.MatchedCount == 0 {
		return utils.NewAppError(utils.ErrNotFound, "Comment not found", nil)
	}

	return nil
}

// Helper function to convert CommentDocument to models.Comment
func convertCommentDocumentToModel(doc *CommentDocument) (*models.Comment, error) {
	id, err := uuid.Parse(doc.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid comment ID: %v", err)
	}

	authorID, err := uuid.Parse(doc.AuthorID)
	if err != nil {
		return nil, fmt.Errorf("invalid author ID: %v", err)
	}

	postID, err := uuid.Parse(doc.PostID)
	if err != nil {
		return nil, fmt.Errorf("invalid post ID: %v", err)
	}

	var parentID *uuid.UUID
	if doc.ParentID != nil {
		parsed, err := uuid.Parse(*doc.ParentID)
		if err != nil {
			return nil, fmt.Errorf("invalid parent ID: %v", err)
		}
		parentID = &parsed
	}

	// Convert Children strings to UUIDs
	children := make([]uuid.UUID, len(doc.Children))
	for i, childIDStr := range doc.Children {
		childID, err := uuid.Parse(childIDStr)
		if err != nil {
			return nil, fmt.Errorf("invalid child ID: %v", err)
		}
		children[i] = childID
	}

	return &models.Comment{
		ID:        id,
		Content:   doc.Content,
		AuthorID:  authorID,
		PostID:    postID,
		ParentID:  parentID,
		Children:  children,
		CreatedAt: doc.CreatedAt,
		UpdatedAt: doc.UpdatedAt,
		IsDeleted: doc.IsDeleted,
		Upvotes:   doc.Upvotes,
		Downvotes: doc.Downvotes,
		Karma:     doc.Karma,
	}, nil
}

// EnsureCommentIndexes creates required indexes for the comments collection
func (m *MongoDB) EnsureCommentIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "postId", Value: 1},
				{Key: "createdAt", Value: -1},
			},
		},
		{
			Keys: bson.D{{Key: "authorId", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "parentId", Value: 1}},
		},
	}

	_, err := m.Comments.Indexes().CreateMany(ctx, indexes)
	if err != nil {
		return fmt.Errorf("failed to create comment indexes: %v", err)
	}

	return nil
}
