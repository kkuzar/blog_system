// internal/database/adapter.go
package database

import (
	"context"
	"errors"

	"github.com/kkuzar/blog_system/internal/config"
	"github.com/kkuzar/blog_system/internal/database/dynamodb"
	"github.com/kkuzar/blog_system/internal/database/firestore"
	"github.com/kkuzar/blog_system/internal/database/mongodb"
	"github.com/kkuzar/blog_system/internal/models"
)

var ErrNotFound = errors.New("item not found")
var ErrDuplicateUser = errors.New("username already exists")
var ErrDBConfig = errors.New("invalid database configuration")

// DBAdapter defines the interface for database operations.
type DBAdapter interface {
	// User operations
	GetUserByUsername(ctx context.Context, username string) (*models.User, error)
	CreateUser(ctx context.Context, user *models.User) error

	// Post operations (Metadata only)
	CreatePostMeta(ctx context.Context, post *models.Post) (string, error) // Returns new post ID
	GetPostMetaByID(ctx context.Context, postID string) (*models.Post, error)
	ListPostMetaByUser(ctx context.Context, userID string, limit, offset int) ([]models.Post, error)
	UpdatePostMeta(ctx context.Context, post *models.Post) error
	DeletePostMeta(ctx context.Context, postID string) error

	// CodeFile operations (Metadata only)
	CreateCodeFileMeta(ctx context.Context, file *models.CodeFile) (string, error) // Returns new file ID
	GetCodeFileMetaByID(ctx context.Context, fileID string) (*models.CodeFile, error)
	ListCodeFileMetaByUser(ctx context.Context, userID string, limit, offset int) ([]models.CodeFile, error)
	UpdateCodeFileMeta(ctx context.Context, file *models.CodeFile) error
	DeleteCodeFileMeta(ctx context.Context, fileID string) error

	// History logging
	LogAction(ctx context.Context, log *models.HistoryLog) (string, error) // Returns log ID
	GetActionHistory(ctx context.Context, itemID string, itemType string, limit int) ([]models.HistoryLog, error)

	// Cleanup
	Close(ctx context.Context) error
}

// NewDBAdapter creates a database adapter based on the configuration.
func NewDBAdapter(ctx context.Context, cfg *config.DBConfig) (DBAdapter, error) {
	switch cfg.Type {
	case "mongodb":
		if cfg.MongoURI == "" || cfg.MongoDBName == "" {
			return nil, errors.New("MongoDB selected but MONGO_URI or MONGO_DB_NAME is missing")
		}
		return mongodb.NewMongoClient(ctx, cfg.MongoURI, cfg.MongoDBName)
	case "dynamodb":
		if cfg.DynamoRegion == "" || cfg.DynamoTable == "" {
			return nil, errors.New("DynamoDB selected but AWS_REGION or DYNAMO_TABLE_NAME is missing")
		}
		return dynamodb.NewDynamoDBClient(ctx, cfg.DynamoRegion, cfg.DynamoTable)
	case "firestore":
		if cfg.FirestoreProjectID == "" {
			// Credentials file path is optional if running in GCP environment with default credentials
			return nil, errors.New("Firestore selected but FIRESTORE_PROJECT_ID is missing")
		}
		return firestore.NewFirestoreClient(ctx, cfg.FirestoreProjectID, cfg.FirestoreCredentials)
	default:
		// Only error if a type is specified but not supported
		if cfg.Type != "" {
			return nil, errors.New("unsupported database type: " + cfg.Type)
		}
		// If no DB type is configured, it's an error for this app
		return nil, errors.New("DB_TYPE must be configured (e.g., 'mongodb', 'dynamodb', 'firestore')")
	}
}
