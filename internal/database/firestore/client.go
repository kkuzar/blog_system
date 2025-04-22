package firestore

import (
	"context"
	"errors"
	"fmt"
	"github.com/kkuzar/blog_system/internal/database"
	"github.com/kkuzar/blog_system/internal/models"
	"log"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	usersCollection     = "users"
	postsCollection     = "posts"
	codefilesCollection = "codefiles"
	historyCollection   = "history"
	defaultLimit        = 50
)

type FirestoreClient struct {
	client *firestore.Client
}

// NewFirestoreClient creates a new Firestore client.
// Connection pooling is handled by the underlying gRPC layer.
func NewFirestoreClient(ctx context.Context, projectID, credentialsFile string) (*FirestoreClient, error) {
	if projectID == "" {
		return nil, errors.New("Firestore project ID is empty")
	}

	var opts []option.ClientOption
	if credentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(credentialsFile))
	}

	client, err := firestore.NewClient(ctx, projectID, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create firestore client: %w", err)
	}

	// Optional: Perform a simple read to confirm connectivity/permissions
	// _, err = client.Collection(usersCollection).Limit(1).Documents(ctx).GetAll()
	// if err != nil {
	// 	_ = client.Close()
	// 	return nil, fmt.Errorf("failed initial firestore read: %w", err)
	// }

	log.Printf("Firestore client initialized for project %s", projectID)

	return &FirestoreClient{
		client: client,
	}, nil
}

// Close closes the Firestore client.
func (c *FirestoreClient) Close(ctx context.Context) error {
	if c.client != nil {
		log.Println("Closing Firestore client...")
		return c.client.Close()
	}
	return nil
}

// --- User Methods ---

func (c *FirestoreClient) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	docSnap, err := c.client.Collection(usersCollection).Doc(username).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, database.ErrNotFound
		}
		log.Printf("Firestore error getting user %s: %v", username, err)
		return nil, err
	}

	var user models.User
	if err := docSnap.DataTo(&user); err != nil {
		log.Printf("Firestore error decoding user %s: %v", username, err)
		return nil, err
	}
	user.ID = docSnap.Ref.ID // Set ID from doc ID
	return &user, nil
}

func (c *FirestoreClient) CreateUser(ctx context.Context, user *models.User) error {
	if user.Username == "" {
		return errors.New("username cannot be empty")
	}
	user.CreatedAt = time.Now().UTC()
	user.ID = user.Username // Use username as doc ID

	// Exclude ID field explicitly if needed, though DataTo usually handles it.
	// Use Create to ensure it doesn't overwrite an existing user.
	_, err := c.client.Collection(usersCollection).Doc(user.Username).Create(ctx, user)
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return database.ErrDuplicateUser
		}
		log.Printf("Firestore error creating user %s: %v", user.Username, err)
		return err
	}
	return nil
}

// --- Post Methods ---

func (c *FirestoreClient) CreatePostMeta(ctx context.Context, post *models.Post) (string, error) {
	collRef := c.client.Collection(postsCollection)
	docRef := collRef.NewDoc() // Auto-generate ID

	post.ID = docRef.ID // Store the generated ID
	post.CreatedAt = time.Now().UTC()
	post.UpdatedAt = post.CreatedAt
	post.Version = 1

	_, err := docRef.Set(ctx, post)
	if err != nil {
		log.Printf("Firestore error creating post meta: %v", err)
		return "", err
	}
	return post.ID, nil
}

func (c *FirestoreClient) GetPostMetaByID(ctx context.Context, postID string) (*models.Post, error) {
	docSnap, err := c.client.Collection(postsCollection).Doc(postID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, database.ErrNotFound
		}
		log.Printf("Firestore error getting post meta %s: %v", postID, err)
		return nil, err
	}
	var post models.Post
	if err := docSnap.DataTo(&post); err != nil {
		log.Printf("Firestore error decoding post %s: %v", postID, err)
		return nil, err
	}
	post.ID = docSnap.Ref.ID
	return &post, nil
}

func (c *FirestoreClient) ListPostMetaByUser(ctx context.Context, userID string, limit, offset int) ([]models.Post, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	query := c.client.Collection(postsCollection).
		Where("UserID", "==", userID).        // Ensure field name matches struct tag exactly
		OrderBy("CreatedAt", firestore.Desc). // Ensure field name matches struct tag
		Limit(limit)

	if offset > 0 {
		query = query.Offset(offset)
	}

	iter := query.Documents(ctx)
	defer iter.Stop()

	var posts []models.Post
	for {
		docSnap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Firestore error iterating posts for user %s: %v", userID, err)
			return nil, err
		}

		var post models.Post
		if err := docSnap.DataTo(&post); err != nil {
			log.Printf("Firestore error decoding post %s in list: %v", docSnap.Ref.ID, err)
			continue
		} // Skip bad doc
		post.ID = docSnap.Ref.ID
		posts = append(posts, post)
	}
	return posts, nil
}

func (c *FirestoreClient) UpdatePostMeta(ctx context.Context, post *models.Post) error {
	docRef := c.client.Collection(postsCollection).Doc(post.ID)

	err := c.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		docSnap, err := tx.Get(docRef) // Use tx.Get
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return database.ErrNotFound
			}
			return err // Propagate other errors
		}

		var existingPost models.Post
		if err := docSnap.DataTo(&existingPost); err != nil {
			return fmt.Errorf("failed to decode existing post in transaction: %w", err)
		}

		// OCC Check
		if existingPost.Version != post.Version {
			return database.ErrVersionMismatch
		}

		// Prepare updates
		updates := []firestore.Update{
			{Path: "Title", Value: post.Title},
			{Path: "Slug", Value: post.Slug},
			{Path: "UpdatedAt", Value: time.Now().UTC()},
			{Path: "S3Path", Value: post.S3Path},
			{Path: "Version", Value: firestore.Increment(1)}, // Increment version
		}

		return tx.Update(docRef, updates) // Use tx.Update
	}) // End Transaction

	if err != nil {
		if errors.Is(err, database.ErrVersionMismatch) || errors.Is(err, database.ErrNotFound) {
			return err // Return specific errors directly
		}
		log.Printf("Firestore transaction error updating post meta %s: %v", post.ID, err)
		// Check if the error is a GRPC error code for concurrency/retry issues
		if stat, ok := status.FromError(err); ok {
			if stat.Code() == codes.Aborted || stat.Code() == codes.FailedPrecondition {
				// Could indicate a concurrent modification detected by Firestore itself
				return database.ErrVersionMismatch // Treat as version mismatch for simplicity
			}
		}
		return err // Return generic transaction error
	}
	return nil
}

func (c *FirestoreClient) DeletePostMeta(ctx context.Context, postID string) error {
	_, err := c.client.Collection(postsCollection).Doc(postID).Delete(ctx)
	if err != nil {
		// Delete doesn't typically return NotFound, but check just in case API changes
		if status.Code(err) == codes.NotFound {
			return database.ErrNotFound
		}
		log.Printf("Firestore error deleting post meta %s: %v", postID, err)
		return err
	}
	// To strictly return ErrNotFound, we'd need a Get before Delete or check error code.
	// For now, assume success unless error occurs.
	return nil
}

// --- CodeFile Methods (Similar structure to Post methods) ---

func (c *FirestoreClient) CreateCodeFileMeta(ctx context.Context, file *models.CodeFile) (string, error) {
	docRef := c.client.Collection(codefilesCollection).NewDoc()
	file.ID = docRef.ID
	file.CreatedAt = time.Now().UTC()
	file.UpdatedAt = file.CreatedAt
	file.Version = 1
	_, err := docRef.Set(ctx, file)
	if err != nil {
		log.Printf("Firestore error creating codefile meta: %v", err)
		return "", err
	}
	return file.ID, nil
}

func (c *FirestoreClient) GetCodeFileMetaByID(ctx context.Context, fileID string) (*models.CodeFile, error) {
	docSnap, err := c.client.Collection(codefilesCollection).Doc(fileID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, database.ErrNotFound
		}
		log.Printf("Firestore error getting codefile meta %s: %v", fileID, err)
		return nil, err
	}
	var file models.CodeFile
	if err := docSnap.DataTo(&file); err != nil {
		log.Printf("Firestore error decoding codefile %s: %v", fileID, err)
		return nil, err
	}
	file.ID = docSnap.Ref.ID
	return &file, nil
}

func (c *FirestoreClient) ListCodeFileMetaByUser(ctx context.Context, userID string, limit, offset int) ([]models.CodeFile, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	query := c.client.Collection(codefilesCollection).
		Where("UserID", "==", userID).
		OrderBy("CreatedAt", firestore.Desc).
		Limit(limit)
	if offset > 0 {
		query = query.Offset(offset)
	}

	iter := query.Documents(ctx)
	defer iter.Stop()
	var files []models.CodeFile
	for {
		docSnap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Firestore error iterating codefiles for user %s: %v", userID, err)
			return nil, err
		}
		var file models.CodeFile
		if err := docSnap.DataTo(&file); err != nil {
			log.Printf("Firestore error decoding codefile %s in list: %v", docSnap.Ref.ID, err)
			continue
		}
		file.ID = docSnap.Ref.ID
		files = append(files, file)
	}
	return files, nil
}

func (c *FirestoreClient) UpdateCodeFileMeta(ctx context.Context, file *models.CodeFile) error {
	docRef := c.client.Collection(codefilesCollection).Doc(file.ID)
	err := c.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		docSnap, err := tx.Get(docRef)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return database.ErrNotFound
			}
			return err
		}
		var existingFile models.CodeFile
		if err := docSnap.DataTo(&existingFile); err != nil {
			return fmt.Errorf("failed to decode existing codefile: %w", err)
		}
		if existingFile.Version != file.Version {
			return database.ErrVersionMismatch
		}

		updates := []firestore.Update{
			{Path: "FileName", Value: file.FileName},
			{Path: "Language", Value: file.Language},
			{Path: "UpdatedAt", Value: time.Now().UTC()},
			{Path: "S3Path", Value: file.S3Path},
			{Path: "Version", Value: firestore.Increment(1)},
		}
		return tx.Update(docRef, updates)
	}) // End Transaction
	if err != nil {
		if errors.Is(err, database.ErrVersionMismatch) || errors.Is(err, database.ErrNotFound) {
			return err
		}
		log.Printf("Firestore transaction error updating codefile meta %s: %v", file.ID, err)
		if stat, ok := status.FromError(err); ok && (stat.Code() == codes.Aborted || stat.Code() == codes.FailedPrecondition) {
			return database.ErrVersionMismatch
		}
		return err
	}
	return nil
}

func (c *FirestoreClient) DeleteCodeFileMeta(ctx context.Context, fileID string) error {
	_, err := c.client.Collection(codefilesCollection).Doc(fileID).Delete(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return database.ErrNotFound
		}
		log.Printf("Firestore error deleting codefile meta %s: %v", fileID, err)
		return err
	}
	return nil
}

// --- History Methods ---

func (c *FirestoreClient) LogAction(ctx context.Context, logEntry *models.HistoryLog) (string, error) {
	docRef := c.client.Collection(historyCollection).NewDoc()
	logEntry.ID = docRef.ID
	if logEntry.Timestamp.IsZero() {
		logEntry.Timestamp = time.Now().UTC()
	}

	_, err := docRef.Set(ctx, logEntry)
	if err != nil {
		log.Printf("Firestore error logging action: %v", err)
		return "", err
	}
	return logEntry.ID, nil
}

func (c *FirestoreClient) GetActionHistory(ctx context.Context, itemID string, itemType string, limit int) ([]models.HistoryLog, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	query := c.client.Collection(historyCollection).
		Where("ItemID", "==", itemID). // Ensure field names match struct tags
		Where("ItemType", "==", itemType).
		OrderBy("Timestamp", firestore.Desc).
		Limit(limit)

	iter := query.Documents(ctx)
	defer iter.Stop()

	var history []models.HistoryLog
	for {
		docSnap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Firestore error iterating history for %s %s: %v", itemType, itemID, err)
			return nil, err
		}

		var logEntry models.HistoryLog
		if err := docSnap.DataTo(&logEntry); err != nil {
			log.Printf("Firestore error decoding history log %s: %v", docSnap.Ref.ID, err)
			continue
		}
		logEntry.ID = docSnap.Ref.ID
		history = append(history, logEntry)
	}
	return history, nil
}

func (c *FirestoreClient) GetHistoryLogByID(ctx context.Context, logID string) (*models.HistoryLog, error) {
	docSnap, err := c.client.Collection(historyCollection).Doc(logID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, database.ErrNotFound
		}
		log.Printf("Firestore error getting history log %s: %v", logID, err)
		return nil, err
	}
	var logEntry models.HistoryLog
	if err := docSnap.DataTo(&logEntry); err != nil {
		log.Printf("Firestore error decoding history log %s: %v", logID, err)
		return nil, err
	}
	logEntry.ID = docSnap.Ref.ID
	return &logEntry, nil
}
