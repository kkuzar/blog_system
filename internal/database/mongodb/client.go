package mongodb

import (
	"context"
	"errors"
	"fmt"
	"github.com/kkuzar/blog_system/internal/database"
	"github.com/kkuzar/blog_system/internal/models"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

const (
	connectTimeout         = 10 * time.Second
	serverSelectionTimeout = 10 * time.Second
	usersCollection        = "users"
	postsCollection        = "posts"
	codefilesCollection    = "codefiles"
	historyCollection      = "history"
)

type MongoClient struct {
	client *mongo.Client
	db     *mongo.Database
}

// NewMongoClient creates a new MongoDB client and establishes connection.
// The driver handles connection pooling internally.
func NewMongoClient(ctx context.Context, uri, dbName string) (*MongoClient, error) {
	if uri == "" || dbName == "" {
		return nil, errors.New("MongoDB URI or Database Name is empty")
	}

	clientOptions := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(connectTimeout).
		SetServerSelectionTimeout(serverSelectionTimeout).
		SetWriteConcern(writeconcern.W1()) // Default write concern

	// You can configure pool size via URI options, e.g., "mongodb://...?maxPoolSize=100"
	// Or programmatically: clientOptions.SetMaxPoolSize(100)

	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mongodb: %w", err)
	}

	// Ping the primary server to verify connection.
	if err := client.Ping(ctx, nil); err != nil {
		// Disconnect if ping fails
		_ = client.Disconnect(context.Background()) // Use background context for cleanup
		return nil, fmt.Errorf("failed to ping mongodb: %w", err)
	}

	log.Println("Successfully connected and pinged MongoDB.")

	db := client.Database(dbName)

	// Optional: Create indexes here if they don't exist
	// createIndexes(ctx, db)

	return &MongoClient{
		client: client,
		db:     db,
	}, nil
}

// Close disconnects the MongoDB client.
func (c *MongoClient) Close(ctx context.Context) error {
	if c.client != nil {
		log.Println("Disconnecting MongoDB client...")
		return c.client.Disconnect(ctx)
	}
	return nil
}

// --- User Methods ---

func (c *MongoClient) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	coll := c.db.Collection(usersCollection)
	var user models.User
	// In this schema, the username is the _id
	err := coll.FindOne(ctx, bson.M{"_id": username}).Decode(&user)
	if err == mongo.ErrNoDocuments {
		return nil, database.ErrNotFound
	} else if err != nil {
		log.Printf("MongoDB error getting user %s: %v", username, err)
		return nil, err
	}
	// Ensure ID field is populated from _id
	user.ID = username
	return &user, nil
}

func (c *MongoClient) CreateUser(ctx context.Context, user *models.User) error {
	if user.Username == "" {
		return errors.New("username cannot be empty")
	}
	coll := c.db.Collection(usersCollection)
	user.CreatedAt = time.Now().UTC()
	// Use username as the document ID (_id)
	doc := bson.M{
		"_id":          user.Username,
		"username":     user.Username,
		"passwordHash": user.PasswordHash,
		"createdAt":    user.CreatedAt,
	}
	_, err := coll.InsertOne(ctx, doc)
	if err != nil {
		// Check for duplicate key error (E11000)
		if mongo.IsDuplicateKeyError(err) {
			return database.ErrDuplicateUser
		}
		log.Printf("MongoDB error creating user %s: %v", user.Username, err)
		return err
	}
	return nil
}

// --- Post Methods ---

func (c *MongoClient) CreatePostMeta(ctx context.Context, post *models.Post) (string, error) {
	coll := c.db.Collection(postsCollection)
	post.ID = primitive.NewObjectID().Hex() // Generate ID here
	post.CreatedAt = time.Now().UTC()
	post.UpdatedAt = post.CreatedAt
	post.Version = 1 // Initial version

	_, err := bson.Marshal(post)
	if err != nil {
		return "", fmt.Errorf("failed to marshal post: %w", err)
	}
	// Use bson.Raw to handle _id correctly if needed, or just insert struct
	_, err = coll.InsertOne(ctx, post) // Insert the struct directly
	if err != nil {
		log.Printf("MongoDB error creating post meta: %v", err)
		return "", err
	}
	return post.ID, nil
}

func (c *MongoClient) GetPostMetaByID(ctx context.Context, postID string) (*models.Post, error) {
	coll := c.db.Collection(postsCollection)
	oid, err := primitive.ObjectIDFromHex(postID)
	if err != nil {
		return nil, fmt.Errorf("invalid post ID format: %w", err)
	}

	var post models.Post
	err = coll.FindOne(ctx, bson.M{"_id": oid}).Decode(&post)
	if err == mongo.ErrNoDocuments {
		return nil, database.ErrNotFound
	} else if err != nil {
		log.Printf("MongoDB error getting post meta %s: %v", postID, err)
		return nil, err
	}
	post.ID = postID // Ensure string ID is set
	return &post, nil
}

func (c *MongoClient) ListPostMetaByUser(ctx context.Context, userID string, limit, offset int) ([]models.Post, error) {
	coll := c.db.Collection(postsCollection)
	findOptions := options.Find().
		SetLimit(int64(limit)).
		SetSkip(int64(offset)).
		SetSort(bson.D{{Key: "createdAt", Value: -1}}) // Sort by newest first

	cursor, err := coll.Find(ctx, bson.M{"userId": userID}, findOptions)
	if err != nil {
		log.Printf("MongoDB error listing posts for user %s: %v", userID, err)
		return nil, err
	}
	defer cursor.Close(ctx)

	var posts []models.Post
	if err = cursor.All(ctx, &posts); err != nil {
		log.Printf("MongoDB error decoding posts for user %s: %v", userID, err)
		return nil, err
	}
	// Ensure string IDs are set
	for i := range posts {
		// Assuming the _id field is correctly unmarshalled by the driver if it's an ObjectID
		// If posts[i].ID is empty, manually convert from the internal _id if needed.
		// This depends on how _id is defined in the struct tag (`bson:"_id,omitempty"`).
		// If _id is ObjectID, we need to convert. Let's assume it is for safety.
		if oid, ok := posts[i].ID.(primitive.ObjectID); ok {
			posts[i].ID = oid.Hex()
		}
	}

	return posts, nil
}

func (c *MongoClient) UpdatePostMeta(ctx context.Context, post *models.Post) error {
	coll := c.db.Collection(postsCollection)
	oid, err := primitive.ObjectIDFromHex(post.ID)
	if err != nil {
		return fmt.Errorf("invalid post ID format: %w", err)
	}

	filter := bson.M{
		"_id":     oid,
		"version": post.Version, // OCC check
	}
	update := bson.M{
		"$set": bson.M{
			"title":     post.Title,
			"slug":      post.Slug,
			"updatedAt": time.Now().UTC(),
			"s3Path":    post.S3Path,
			// Add other updatable fields here
		},
		"$inc": bson.M{"version": 1}, // Increment version atomically
	}

	result, err := coll.UpdateOne(ctx, filter, update)
	if err != nil {
		log.Printf("MongoDB error updating post meta %s: %v", post.ID, err)
		return err
	}

	if result.MatchedCount == 0 {
		// Check if the document exists at all with a different version
		existsCount, _ := coll.CountDocuments(ctx, bson.M{"_id": oid})
		if existsCount > 0 {
			return database.ErrVersionMismatch
		}
		return database.ErrNotFound // Or version mismatch if exists? Let's stick to mismatch if exists.
	}
	if result.ModifiedCount == 0 {
		// This might happen if the update data is identical to existing data,
		// but version should still increment. Log a warning if needed.
		// log.Printf("MongoDB update for post %s matched but didn't modify (version still incremented).", post.ID)
	}

	return nil
}

func (c *MongoClient) DeletePostMeta(ctx context.Context, postID string) error {
	coll := c.db.Collection(postsCollection)
	oid, err := primitive.ObjectIDFromHex(postID)
	if err != nil {
		return fmt.Errorf("invalid post ID format: %w", err)
	}

	result, err := coll.DeleteOne(ctx, bson.M{"_id": oid})
	if err != nil {
		log.Printf("MongoDB error deleting post meta %s: %v", postID, err)
		return err
	}
	if result.DeletedCount == 0 {
		// Optional: Check if it existed before returning not found
		return database.ErrNotFound
	}
	return nil
}

// --- CodeFile Methods (Similar structure to Post methods) ---

func (c *MongoClient) CreateCodeFileMeta(ctx context.Context, file *models.CodeFile) (string, error) {
	coll := c.db.Collection(codefilesCollection)
	file.ID = primitive.NewObjectID().Hex()
	file.CreatedAt = time.Now().UTC()
	file.UpdatedAt = file.CreatedAt
	file.Version = 1

	_, err := coll.InsertOne(ctx, file)
	if err != nil {
		log.Printf("MongoDB error creating codefile meta: %v", err)
		return "", err
	}
	return file.ID, nil
}

func (c *MongoClient) GetCodeFileMetaByID(ctx context.Context, fileID string) (*models.CodeFile, error) {
	coll := c.db.Collection(codefilesCollection)
	oid, err := primitive.ObjectIDFromHex(fileID)
	if err != nil {
		return nil, fmt.Errorf("invalid codefile ID format: %w", err)
	}

	var file models.CodeFile
	err = coll.FindOne(ctx, bson.M{"_id": oid}).Decode(&file)
	if err == mongo.ErrNoDocuments {
		return nil, database.ErrNotFound
	}
	if err != nil {
		log.Printf("MongoDB error getting codefile meta %s: %v", fileID, err)
		return nil, err
	}
	file.ID = fileID
	return &file, nil
}

func (c *MongoClient) ListCodeFileMetaByUser(ctx context.Context, userID string, limit, offset int) ([]models.CodeFile, error) {
	coll := c.db.Collection(codefilesCollection)
	findOptions := options.Find().
		SetLimit(int64(limit)).
		SetSkip(int64(offset)).
		SetSort(bson.D{{Key: "createdAt", Value: -1}})

	cursor, err := coll.Find(ctx, bson.M{"userId": userID}, findOptions)
	if err != nil {
		log.Printf("MongoDB error listing codefiles for user %s: %v", userID, err)
		return nil, err
	}
	defer cursor.Close(ctx)

	var files []models.CodeFile
	if err = cursor.All(ctx, &files); err != nil {
		log.Printf("MongoDB error decoding codefiles for user %s: %v", userID, err)
		return nil, err
	}
	// Ensure string IDs are set
	for i := range files {
		if oid, ok := files[i].ID.(primitive.ObjectID); ok {
			files[i].ID = oid.Hex()
		}
	}
	return files, nil
}

func (c *MongoClient) UpdateCodeFileMeta(ctx context.Context, file *models.CodeFile) error {
	coll := c.db.Collection(codefilesCollection)
	oid, err := primitive.ObjectIDFromHex(file.ID)
	if err != nil {
		return fmt.Errorf("invalid codefile ID format: %w", err)
	}

	filter := bson.M{"_id": oid, "version": file.Version}
	update := bson.M{
		"$set": bson.M{
			"fileName":  file.FileName,
			"language":  file.Language,
			"updatedAt": time.Now().UTC(),
			"s3Path":    file.S3Path,
		},
		"$inc": bson.M{"version": 1},
	}

	result, err := coll.UpdateOne(ctx, filter, update)
	if err != nil {
		log.Printf("MongoDB error updating codefile meta %s: %v", file.ID, err)
		return err
	}

	if result.MatchedCount == 0 {
		existsCount, _ := coll.CountDocuments(ctx, bson.M{"_id": oid})
		if existsCount > 0 {
			return database.ErrVersionMismatch
		}
		return database.ErrNotFound
	}
	return nil
}

func (c *MongoClient) DeleteCodeFileMeta(ctx context.Context, fileID string) error {
	coll := c.db.Collection(codefilesCollection)
	oid, err := primitive.ObjectIDFromHex(fileID)
	if err != nil {
		return fmt.Errorf("invalid codefile ID format: %w", err)
	}

	result, err := coll.DeleteOne(ctx, bson.M{"_id": oid})
	if err != nil {
		log.Printf("MongoDB error deleting codefile meta %s: %v", fileID, err)
		return err
	}
	if result.DeletedCount == 0 {
		return database.ErrNotFound
	}
	return nil
}

// --- History Methods ---

func (c *MongoClient) LogAction(ctx context.Context, logEntry *models.HistoryLog) (string, error) {
	coll := c.db.Collection(historyCollection)
	logEntry.ID = primitive.NewObjectID().Hex() // Generate ID
	// Ensure timestamp is set if not already
	if logEntry.Timestamp.IsZero() {
		logEntry.Timestamp = time.Now().UTC()
	}

	_, err := coll.InsertOne(ctx, logEntry)
	if err != nil {
		log.Printf("MongoDB error logging action: %v", err)
		return "", err
	}
	return logEntry.ID, nil
}

func (c *MongoClient) GetActionHistory(ctx context.Context, itemID string, itemType string, limit int) ([]models.HistoryLog, error) {
	coll := c.db.Collection(historyCollection)
	findOptions := options.Find().
		SetSort(bson.D{{Key: "timestamp", Value: -1}}) // Newest first
	if limit > 0 {
		findOptions.SetLimit(int64(limit))
	}

	filter := bson.M{"itemId": itemID, "itemType": itemType}

	cursor, err := coll.Find(ctx, filter, findOptions)
	if err != nil {
		log.Printf("MongoDB error getting history for %s %s: %v", itemType, itemID, err)
		return nil, err
	}
	defer cursor.Close(ctx)

	var history []models.HistoryLog
	if err = cursor.All(ctx, &history); err != nil {
		log.Printf("MongoDB error decoding history for %s %s: %v", itemType, itemID, err)
		return nil, err
	}
	// Ensure string IDs are set
	for i := range history {
		if oid, ok := history[i].ID.(primitive.ObjectID); ok {
			history[i].ID = oid.Hex()
		}
	}
	return history, nil
}

func (c *MongoClient) GetHistoryLogByID(ctx context.Context, logID string) (*models.HistoryLog, error) {
	coll := c.db.Collection(historyCollection)
	oid, err := primitive.ObjectIDFromHex(logID)
	if err != nil {
		return nil, fmt.Errorf("invalid history log ID format: %w", err)
	}

	var logEntry models.HistoryLog
	err = coll.FindOne(ctx, bson.M{"_id": oid}).Decode(&logEntry)
	if err == mongo.ErrNoDocuments {
		return nil, database.ErrNotFound
	} else if err != nil {
		log.Printf("MongoDB error getting history log %s: %v", logID, err)
		return nil, err
	}
	logEntry.ID = logID
	return &logEntry, nil
}

// Optional: Helper function to create indexes
// func createIndexes(ctx context.Context, db *mongo.Database) {
// 	// Example: Index for listing posts/codefiles by user
// 	indexModels := []mongo.IndexModel{
// 		{ Keys: bson.D{{Key: "userId", Value: 1}, {Key: "createdAt", Value: -1}} },
// 		// Add other indexes as needed (e.g., history itemId/itemType/timestamp)
// 		{ Keys: bson.D{{Key: "itemId", Value: 1}, {Key: "itemType", Value: 1}, {Key: "timestamp", Value: -1}} },
// 	}
// 	_, err := db.Collection(postsCollection).Indexes().CreateMany(ctx, indexModels)
// 	if err != nil { log.Printf("Failed to create indexes for posts: %v", err) }
// 	_, err = db.Collection(codefilesCollection).Indexes().CreateMany(ctx, indexModels)
// 	if err != nil { log.Printf("Failed to create indexes for codefiles: %v", err) }
// 	_, err = db.Collection(historyCollection).Indexes().CreateOne(ctx, mongo.IndexModel{
// 		Keys: bson.D{{Key: "itemId", Value: 1}, {Key: "itemType", Value: 1}, {Key: "timestamp", Value: -1}},
// 	})
// 	if err != nil { log.Printf("Failed to create index for history: %v", err) }
// }
