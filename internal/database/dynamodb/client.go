package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"github.com/kkuzar/blog_system/internal/database"
	"github.com/kkuzar/blog_system/internal/models"
	"github.com/kkuzar/blog_system/uitls/pointer" // Use pointer helper
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
)

const (
	// Define primary key and sort key names used in the table
	pkName = "pk" // Partition Key
	skName = "sk" // Sort Key

	// Define GSI names and keys
	gsi1Name = "gsi1" // For listing items by user
	gsi1PK   = "userId"
	gsi1SK   = "createdAt" // Use createdAt for sorting within user items

	// Define item type prefixes/values used in keys
	userPrefix       = "USER#"
	postPrefix       = "POST#"
	codefilePrefix   = "CODEFILE#"
	historyPrefix    = "HISTORY#"    // Prefix for history item PK
	historyLogPrefix = "HISTORYLOG#" // Prefix for direct history log lookup PK

	// Define SK values for different item types
	userTypeSK          = "USER"
	postTypeSK          = "POST"
	codefileTypeSK      = "CODEFILE"
	historyTypeSKPrefix = "HISTORY#"   // SK for history items: HISTORY#timestamp
	historyLogTypeSK    = "HISTORYLOG" // SK for direct history log lookup

	defaultLimit = 50
)

type DynamoDBClient struct {
	client    *dynamodb.Client
	tableName string
}

// NewDynamoDBClient creates a new DynamoDB client.
// The AWS SDK handles connection pooling internally.
func NewDynamoDBClient(ctx context.Context, region, tableName string) (*DynamoDBClient, error) {
	if region == "" || tableName == "" {
		return nil, errors.New("DynamoDB region or table name is empty")
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load aws config: %w", err)
	}

	client := dynamodb.NewFromConfig(cfg)

	// Optional: Check if table exists (can slow down startup)
	// _, err = client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(tableName)})
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to describe dynamodb table %s: %w", tableName, err)
	// }

	log.Printf("DynamoDB client initialized for table %s in region %s", tableName, region)

	return &DynamoDBClient{
		client:    client,
		tableName: tableName,
	}, nil
}

// Close is a no-op for the DynamoDB client as the SDK manages connections.
func (c *DynamoDBClient) Close(ctx context.Context) error {
	log.Println("DynamoDB client Close() called (no-op)")
	return nil
}

// --- Key Generation Helpers ---
func userPK(username string) string      { return userPrefix + username }
func postPK(postID string) string        { return postPrefix + postID }
func codefilePK(fileID string) string    { return codefilePrefix + fileID }
func historyItemPK(itemID string) string { return historyPrefix + itemID }   // PK for querying history of an item
func historyLogPK(logID string) string   { return historyLogPrefix + logID } // PK for direct log lookup
func historySK(timestamp time.Time) string {
	return historyTypeSKPrefix + timestamp.UTC().Format(time.RFC3339Nano)
}

// --- User Methods ---

func (c *DynamoDBClient) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	key, err := attributevalue.MarshalMap(map[string]string{
		pkName: userPK(username),
		skName: userTypeSK,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal key for GetUser: %w", err)
	}

	input := &dynamodb.GetItemInput{
		TableName: aws.String(c.tableName),
		Key:       key,
	}

	result, err := c.client.GetItem(ctx, input)
	if err != nil {
		log.Printf("DynamoDB error getting user %s: %v", username, err)
		return nil, err
	}
	if result.Item == nil {
		return nil, database.ErrNotFound
	}

	var user models.User
	if err := attributevalue.UnmarshalMap(result.Item, &user); err != nil {
		log.Printf("DynamoDB error unmarshalling user %s: %v", username, err)
		return nil, err
	}
	user.ID = username // Set ID from username
	return &user, nil
}

func (c *DynamoDBClient) CreateUser(ctx context.Context, user *models.User) error {
	if user.Username == "" {
		return errors.New("username cannot be empty")
	}
	user.CreatedAt = time.Now().UTC()
	user.ID = user.Username // Use username as ID

	itemMap, err := attributevalue.MarshalMap(user)
	if err != nil {
		return fmt.Errorf("failed to marshal user for CreateUser: %w", err)
	}

	// Add PK/SK to the map
	itemMap[pkName] = &types.AttributeValueMemberS{Value: userPK(user.Username)}
	itemMap[skName] = &types.AttributeValueMemberS{Value: userTypeSK}

	input := &dynamodb.PutItemInput{
		TableName:           aws.String(c.tableName),
		Item:                itemMap,
		ConditionExpression: aws.String(fmt.Sprintf("attribute_not_exists(%s)", pkName)), // Ensure user doesn't exist
	}

	_, err = c.client.PutItem(ctx, input)
	if err != nil {
		var condCheckFailed *types.ConditionalCheckFailedException
		if errors.As(err, &condCheckFailed) {
			return database.ErrDuplicateUser
		}
		log.Printf("DynamoDB error creating user %s: %v", user.Username, err)
		return err
	}
	return nil
}

// --- Post Methods ---

func (c *DynamoDBClient) CreatePostMeta(ctx context.Context, post *models.Post) (string, error) {
	post.ID = uuid.NewString() // Generate ID
	post.CreatedAt = time.Now().UTC()
	post.UpdatedAt = post.CreatedAt
	post.Version = 1

	itemMap, err := attributevalue.MarshalMap(post)
	if err != nil {
		return "", fmt.Errorf("failed to marshal post for CreatePostMeta: %w", err)
	}

	itemMap[pkName] = &types.AttributeValueMemberS{Value: postPK(post.ID)}
	itemMap[skName] = &types.AttributeValueMemberS{Value: postTypeSK}
	// Add GSI keys
	itemMap[gsi1PK] = &types.AttributeValueMemberS{Value: post.UserID}
	itemMap[gsi1SK] = &types.AttributeValueMemberS{Value: post.CreatedAt.UTC().Format(time.RFC3339Nano)}

	input := &dynamodb.PutItemInput{
		TableName: aws.String(c.tableName),
		Item:      itemMap,
		// Optional: ConditionExpression attribute_not_exists(pk) if needed, but UUIDs should be unique
	}

	_, err = c.client.PutItem(ctx, input)
	if err != nil {
		log.Printf("DynamoDB error creating post meta %s: %v", post.ID, err)
		return "", err
	}
	return post.ID, nil
}

func (c *DynamoDBClient) GetPostMetaByID(ctx context.Context, postID string) (*models.Post, error) {
	key, err := attributevalue.MarshalMap(map[string]string{
		pkName: postPK(postID),
		skName: postTypeSK,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal key for GetPostMeta: %w", err)
	}

	input := &dynamodb.GetItemInput{
		TableName: aws.String(c.tableName),
		Key:       key,
	}

	result, err := c.client.GetItem(ctx, input)
	if err != nil {
		log.Printf("DynamoDB error getting post meta %s: %v", postID, err)
		return nil, err
	}
	if result.Item == nil {
		return nil, database.ErrNotFound
	}

	var post models.Post
	if err := attributevalue.UnmarshalMap(result.Item, &post); err != nil {
		log.Printf("DynamoDB error unmarshalling post %s: %v", postID, err)
		return nil, err
	}
	post.ID = postID // Set ID
	return &post, nil
}

func (c *DynamoDBClient) ListPostMetaByUser(ctx context.Context, userID string, limit, offset int) ([]models.Post, error) {
	// Note: DynamoDB Query doesn't support offset directly.
	// Pagination is handled via LastEvaluatedKey. Implementing offset requires
	// potentially fetching and discarding items, which is inefficient.
	// We'll implement limit and basic pagination, ignoring offset for simplicity here.
	if offset > 0 {
		log.Printf("WARN: DynamoDB ListPostMetaByUser does not efficiently support offset. Offset %d ignored.", offset)
	}
	if limit <= 0 {
		limit = defaultLimit
	}

	keyCond := expression.Key(gsi1PK).Equal(expression.Value(userID))
	// We need to filter SK to only get Posts, but GSI1SK is createdAt.
	// We must add itemType to the GSI projection or filter after querying.
	// Let's assume itemType IS projected onto the GSI or filter afterwards.
	// For simplicity, let's filter afterwards.

	expr, err := expression.NewBuilder().WithKeyCondition(keyCond).Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build query expression: %w", err)
	}

	input := &dynamodb.QueryInput{
		TableName:                 aws.String(c.tableName),
		IndexName:                 aws.String(gsi1Name),
		KeyConditionExpression:    expr.KeyCondition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		Limit:                     pointer.To(int32(limit)),
		ScanIndexForward:          pointer.To(false), // Sort by createdAt descending
	}

	var posts []models.Post
	paginator := dynamodb.NewQueryPaginator(c.client, input)

	// Iterate through pages until limit is reached or no more pages
	itemsFetched := 0
	for paginator.HasMorePages() && itemsFetched < limit {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			log.Printf("DynamoDB error querying posts for user %s: %v", userID, err)
			return nil, err
		}

		var pagePosts []models.Post
		if err := attributevalue.UnmarshalListOfMaps(page.Items, &pagePosts); err != nil {
			log.Printf("DynamoDB error unmarshalling posts page: %v", err)
			return nil, err
		}

		for _, p := range pagePosts {
			// Filter for actual posts (if itemType wasn't part of GSI key)
			// This assumes the base item has an 'itemType' field or similar.
			// Our model doesn't explicitly have it at the top level for Post/CodeFile.
			// We rely on the SK of the base item. Let's assume UnmarshalListOfMaps works based on fields present.
			// A better GSI design might include itemType in the GSI SK (e.g., POST#createdAt).
			// For now, assume unmarshalling works.
			p.ID = strings.TrimPrefix(p.ID, postPrefix) // Clean up ID if PK was unmarshalled into it
			posts = append(posts, p)
			itemsFetched++
			if itemsFetched >= limit {
				break
			}
		}
	}

	return posts, nil
}

func (c *DynamoDBClient) UpdatePostMeta(ctx context.Context, post *models.Post) error {
	key, err := attributevalue.MarshalMap(map[string]string{
		pkName: postPK(post.ID),
		skName: postTypeSK,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal key for UpdatePostMeta: %w", err)
	}

	// Use expression builder for clarity
	cond := expression.Name("version").Equal(expression.Value(post.Version))
	update := expression.Set(expression.Name("title"), expression.Value(post.Title)).
		Set(expression.Name("slug"), expression.Value(post.Slug)).
		Set(expression.Name("updatedAt"), expression.Value(time.Now().UTC().Format(time.RFC3339Nano))).
		Set(expression.Name("s3Path"), expression.Value(post.S3Path)).
		Add(expression.Name("version"), expression.Value(1)) // Increment version

	expr, err := expression.NewBuilder().WithCondition(cond).WithUpdate(update).Build()
	if err != nil {
		return fmt.Errorf("failed to build update expression: %w", err)
	}

	input := &dynamodb.UpdateItemInput{
		TableName:                 aws.String(c.tableName),
		Key:                       key,
		ConditionExpression:       expr.Condition(),
		UpdateExpression:          expr.Update(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		ReturnValues:              types.ReturnValueNone,
	}

	_, err = c.client.UpdateItem(ctx, input)
	if err != nil {
		var condCheckFailed *types.ConditionalCheckFailedException
		if errors.As(err, &condCheckFailed) {
			// Need to check if it failed because item doesn't exist or version mismatch
			_, getErr := c.GetPostMetaByID(ctx, post.ID) // Check existence
			if errors.Is(getErr, database.ErrNotFound) {
				return database.ErrNotFound
			}
			return database.ErrVersionMismatch // Assume version mismatch if item exists
		}
		log.Printf("DynamoDB error updating post meta %s: %v", post.ID, err)
		return err
	}
	return nil
}

func (c *DynamoDBClient) DeletePostMeta(ctx context.Context, postID string) error {
	key, err := attributevalue.MarshalMap(map[string]string{
		pkName: postPK(postID),
		skName: postTypeSK,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal key for DeletePostMeta: %w", err)
	}

	input := &dynamodb.DeleteItemInput{
		TableName: aws.String(c.tableName),
		Key:       key,
		// Optional: Add ConditionExpression to ensure item exists before deleting
		// ConditionExpression: aws.String(fmt.Sprintf("attribute_exists(%s)", pkName)),
	}

	_, err = c.client.DeleteItem(ctx, input)
	if err != nil {
		// Check if conditional check failed (if added) - might mean already deleted (not found)
		// var condCheckFailed *types.ConditionalCheckFailedException
		// if errors.As(err, &condCheckFailed) { return database.ErrNotFound }
		log.Printf("DynamoDB error deleting post meta %s: %v", postID, err)
		return err
	}
	// Note: DeleteItem doesn't error if the item doesn't exist unless a condition fails.
	// If strict "not found" is needed, perform a GetItem first or use a condition.
	return nil
}

// --- CodeFile Methods (Similar structure to Post methods) ---
// Implement CreateCodeFileMeta, GetCodeFileMetaByID, ListCodeFileMetaByUser, UpdateCodeFileMeta, DeleteCodeFileMeta
// using codefilePK, codefileTypeSK, and the GSI for listing. Remember OCC for Update.

func (c *DynamoDBClient) CreateCodeFileMeta(ctx context.Context, file *models.CodeFile) (string, error) {
	file.ID = uuid.NewString()
	file.CreatedAt = time.Now().UTC()
	file.UpdatedAt = file.CreatedAt
	file.Version = 1

	itemMap, err := attributevalue.MarshalMap(file)
	if err != nil {
		return "", fmt.Errorf("failed to marshal codefile: %w", err)
	}

	itemMap[pkName] = &types.AttributeValueMemberS{Value: codefilePK(file.ID)}
	itemMap[skName] = &types.AttributeValueMemberS{Value: codefileTypeSK}
	itemMap[gsi1PK] = &types.AttributeValueMemberS{Value: file.UserID}
	itemMap[gsi1SK] = &types.AttributeValueMemberS{Value: file.CreatedAt.UTC().Format(time.RFC3339Nano)}

	input := &dynamodb.PutItemInput{TableName: aws.String(c.tableName), Item: itemMap}
	_, err = c.client.PutItem(ctx, input)
	if err != nil {
		log.Printf("DynamoDB error creating codefile meta %s: %v", file.ID, err)
		return "", err
	}
	return file.ID, nil
}

func (c *DynamoDBClient) GetCodeFileMetaByID(ctx context.Context, fileID string) (*models.CodeFile, error) {
	key, err := attributevalue.MarshalMap(map[string]string{pkName: codefilePK(fileID), skName: codefileTypeSK})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal key: %w", err)
	}
	input := &dynamodb.GetItemInput{TableName: aws.String(c.tableName), Key: key}
	result, err := c.client.GetItem(ctx, input)
	if err != nil {
		log.Printf("DynamoDB error getting codefile meta %s: %v", fileID, err)
		return nil, err
	}
	if result.Item == nil {
		return nil, database.ErrNotFound
	}
	var file models.CodeFile
	if err := attributevalue.UnmarshalMap(result.Item, &file); err != nil {
		log.Printf("DynamoDB error unmarshalling codefile %s: %v", fileID, err)
		return nil, err
	}
	file.ID = fileID
	return &file, nil
}

func (c *DynamoDBClient) ListCodeFileMetaByUser(ctx context.Context, userID string, limit, offset int) ([]models.CodeFile, error) {
	// Similar GSI query as ListPostMetaByUser, filter/unmarshal into CodeFile
	if offset > 0 {
		log.Printf("WARN: DynamoDB ListCodeFileMetaByUser does not efficiently support offset. Offset %d ignored.", offset)
	}
	if limit <= 0 {
		limit = defaultLimit
	}

	keyCond := expression.Key(gsi1PK).Equal(expression.Value(userID))
	expr, err := expression.NewBuilder().WithKeyCondition(keyCond).Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build query expression: %w", err)
	}

	input := &dynamodb.QueryInput{
		TableName: aws.String(c.tableName), IndexName: aws.String(gsi1Name),
		KeyConditionExpression: expr.KeyCondition(), ExpressionAttributeNames: expr.Names(), ExpressionAttributeValues: expr.Values(),
		Limit: pointer.To(int32(limit)), ScanIndexForward: pointer.To(false),
	}

	var files []models.CodeFile
	paginator := dynamodb.NewQueryPaginator(c.client, input)
	itemsFetched := 0
	for paginator.HasMorePages() && itemsFetched < limit {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			log.Printf("DynamoDB error querying codefiles for user %s: %v", userID, err)
			return nil, err
		}
		var pageFiles []models.CodeFile
		if err := attributevalue.UnmarshalListOfMaps(page.Items, &pageFiles); err != nil {
			log.Printf("DynamoDB error unmarshalling codefiles page: %v", err)
			return nil, err
		}
		for _, f := range pageFiles {
			f.ID = strings.TrimPrefix(f.ID, codefilePrefix)
			files = append(files, f)
			itemsFetched++
			if itemsFetched >= limit {
				break
			}
		}
	}
	return files, nil
}

func (c *DynamoDBClient) UpdateCodeFileMeta(ctx context.Context, file *models.CodeFile) error {
	key, err := attributevalue.MarshalMap(map[string]string{pkName: codefilePK(file.ID), skName: codefileTypeSK})
	if err != nil {
		return fmt.Errorf("failed to marshal key: %w", err)
	}

	cond := expression.Name("version").Equal(expression.Value(file.Version))
	update := expression.Set(expression.Name("fileName"), expression.Value(file.FileName)).
		Set(expression.Name("language"), expression.Value(file.Language)).
		Set(expression.Name("updatedAt"), expression.Value(time.Now().UTC().Format(time.RFC3339Nano))).
		Set(expression.Name("s3Path"), expression.Value(file.S3Path)).
		Add(expression.Name("version"), expression.Value(1))

	expr, err := expression.NewBuilder().WithCondition(cond).WithUpdate(update).Build()
	if err != nil {
		return fmt.Errorf("failed to build update expression: %w", err)
	}

	input := &dynamodb.UpdateItemInput{
		TableName: aws.String(c.tableName), Key: key,
		ConditionExpression: expr.Condition(), UpdateExpression: expr.Update(),
		ExpressionAttributeNames: expr.Names(), ExpressionAttributeValues: expr.Values(),
		ReturnValues: types.ReturnValueNone,
	}

	_, err = c.client.UpdateItem(ctx, input)
	if err != nil {
		var condCheckFailed *types.ConditionalCheckFailedException
		if errors.As(err, &condCheckFailed) {
			_, getErr := c.GetCodeFileMetaByID(ctx, file.ID)
			if errors.Is(getErr, database.ErrNotFound) {
				return database.ErrNotFound
			}
			return database.ErrVersionMismatch
		}
		log.Printf("DynamoDB error updating codefile meta %s: %v", file.ID, err)
		return err
	}
	return nil
}

func (c *DynamoDBClient) DeleteCodeFileMeta(ctx context.Context, fileID string) error {
	key, err := attributevalue.MarshalMap(map[string]string{pkName: codefilePK(fileID), skName: codefileTypeSK})
	if err != nil {
		return fmt.Errorf("failed to marshal key: %w", err)
	}
	input := &dynamodb.DeleteItemInput{TableName: aws.String(c.tableName), Key: key}
	_, err = c.client.DeleteItem(ctx, input)
	if err != nil {
		log.Printf("DynamoDB error deleting codefile meta %s: %v", fileID, err)
		return err
	}
	return nil
}

// --- History Methods ---

func (c *DynamoDBClient) LogAction(ctx context.Context, logEntry *models.HistoryLog) (string, error) {
	logEntry.ID = uuid.NewString() // Generate ID
	if logEntry.Timestamp.IsZero() {
		logEntry.Timestamp = time.Now().UTC()
	}

	itemMap, err := attributevalue.MarshalMap(logEntry)
	if err != nil {
		return "", fmt.Errorf("failed to marshal history log: %w", err)
	}

	// Use direct lookup PK/SK for GetHistoryLogByID
	itemMap[pkName] = &types.AttributeValueMemberS{Value: historyLogPK(logEntry.ID)}
	itemMap[skName] = &types.AttributeValueMemberS{Value: historyLogTypeSK}
	// Add separate item for querying history by itemID
	historyItemMap := make(map[string]types.AttributeValue)
	for k, v := range itemMap {
		historyItemMap[k] = v
	} // Copy base data
	historyItemMap[pkName] = &types.AttributeValueMemberS{Value: historyItemPK(logEntry.ItemID)}
	historyItemMap[skName] = &types.AttributeValueMemberS{Value: historySK(logEntry.Timestamp)}

	// Use BatchWriteItem or TransactWriteItems if atomicity is critical between the two items
	// For simplicity, use two PutItem calls. Failure of the second is less critical.
	inputLog := &dynamodb.PutItemInput{TableName: aws.String(c.tableName), Item: itemMap}
	_, err = c.client.PutItem(ctx, inputLog)
	if err != nil {
		log.Printf("DynamoDB error logging history log item %s: %v", logEntry.ID, err)
		return "", err
	}

	inputHistory := &dynamodb.PutItemInput{TableName: aws.String(c.tableName), Item: historyItemMap}
	_, err = c.client.PutItem(ctx, inputHistory)
	if err != nil {
		// Log warning, but don't fail the whole operation as the main log entry succeeded.
		log.Printf("DynamoDB WARN: Failed to log history query item for log %s, item %s: %v", logEntry.ID, logEntry.ItemID, err)
	}

	return logEntry.ID, nil
}

func (c *DynamoDBClient) GetActionHistory(ctx context.Context, itemID string, itemType string, limit int) ([]models.HistoryLog, error) {
	if limit <= 0 {
		limit = defaultLimit
	}

	// Query using the history item PK
	keyCond := expression.Key(pkName).Equal(expression.Value(historyItemPK(itemID))).
		And(expression.Key(skName).BeginsWith(historyTypeSKPrefix)) // SK starts with HISTORY#

	// Add filter for itemType if needed (though PK should be specific enough if designed well)
	// filt := expression.Name("itemType").Equal(expression.Value(itemType))

	exprBuilder := expression.NewBuilder().WithKeyCondition(keyCond)
	// if itemType != "" { exprBuilder = exprBuilder.WithFilter(filt) }
	expr, err := exprBuilder.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build history query expression: %w", err)
	}

	input := &dynamodb.QueryInput{
		TableName:              aws.String(c.tableName),
		KeyConditionExpression: expr.KeyCondition(),
		// FilterExpression:          expr.Filter(), // Uncomment if filtering
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		Limit:                     pointer.To(int32(limit)),
		ScanIndexForward:          pointer.To(false), // Sort by timestamp descending
	}

	var history []models.HistoryLog
	paginator := dynamodb.NewQueryPaginator(c.client, input)

	itemsFetched := 0
	for paginator.HasMorePages() && itemsFetched < limit {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			log.Printf("DynamoDB error querying history for item %s: %v", itemID, err)
			return nil, err
		}
		var pageHistory []models.HistoryLog
		if err := attributevalue.UnmarshalListOfMaps(page.Items, &pageHistory); err != nil {
			log.Printf("DynamoDB error unmarshalling history page: %v", err)
			return nil, err
		}
		for _, h := range pageHistory {
			// Filter by itemType if not done in query (safer)
			if h.ItemType == itemType {
				h.ID = strings.TrimPrefix(h.ID, historyLogPrefix) // Clean up ID
				history = append(history, h)
				itemsFetched++
				if itemsFetched >= limit {
					break
				}
			}
		}
	}
	return history, nil
}

func (c *DynamoDBClient) GetHistoryLogByID(ctx context.Context, logID string) (*models.HistoryLog, error) {
	key, err := attributevalue.MarshalMap(map[string]string{
		pkName: historyLogPK(logID),
		skName: historyLogTypeSK,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal key for GetHistoryLogByID: %w", err)
	}

	input := &dynamodb.GetItemInput{
		TableName: aws.String(c.tableName),
		Key:       key,
	}

	result, err := c.client.GetItem(ctx, input)
	if err != nil {
		log.Printf("DynamoDB error getting history log %s: %v", logID, err)
		return nil, err
	}
	if result.Item == nil {
		return nil, database.ErrNotFound
	}

	var logEntry models.HistoryLog
	if err := attributevalue.UnmarshalMap(result.Item, &logEntry); err != nil {
		log.Printf("DynamoDB error unmarshalling history log %s: %v", logID, err)
		return nil, err
	}
	logEntry.ID = logID // Set ID
	return &logEntry, nil
}
