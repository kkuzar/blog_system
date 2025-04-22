package models

import (
	"time"
)

// HistoryAction defines the type of action logged.
type HistoryAction string

// ... (ItemType, User, Post, CodeFile, Change, HistoryAction constants remain same) ...
// Add ActionRevert constant
const (
	ActionCreate   HistoryAction = "create"
	ActionDelete   HistoryAction = "delete"
	ActionPatch    HistoryAction = "patch"
	ActionSnapshot HistoryAction = "snapshot"
	ActionRevert   HistoryAction = "revert" // Added
)

type HistoryLog struct {
	ID         string        `json:"id" bson:"_id,omitempty" dynamodbav:"id" firestore:"-"`
	UserID     string        `json:"userId" bson:"userId" dynamodbav:"userId" firestore:"userId"`
	ItemID     string        `json:"itemId" bson:"itemId" dynamodbav:"itemId" firestore:"itemId"`
	ItemType   string        `json:"itemType" bson:"itemType" dynamodbav:"itemType" firestore:"itemType"`
	Action     HistoryAction `json:"action" bson:"action" dynamodbav:"action" firestore:"action"`
	Timestamp  time.Time     `json:"timestamp" bson:"timestamp" dynamodbav:"timestamp" firestore:"timestamp"`
	ChangeData *Change       `json:"changeData,omitempty" bson:"changeData,omitempty" dynamodbav:"changeData,omitempty" firestore:"changeData,omitempty"`
	// S3PathBefore/After store the path *relevant to the action*
	S3PathBefore string `json:"-" bson:"s3PathBefore,omitempty" dynamodbav:"s3PathBefore,omitempty" firestore:"s3PathBefore,omitempty"` // Path before delete/revert
	S3PathAfter  string `json:"-" bson:"s3PathAfter,omitempty" dynamodbav:"s3PathAfter,omitempty" firestore:"s3PathAfter,omitempty"`    // Path after create/snapshot/revert
	ItemVersion  int    `json:"itemVersion" bson:"itemVersion" dynamodbav:"itemVersion" firestore:"itemVersion"`
	// Optional: Add field to link revert action to the log entry being reverted to
	RevertedToLogID *string `json:"revertedToLogId,omitempty" bson:"revertedToLogId,omitempty" dynamodbav:"revertedToLogId,omitempty" firestore:"revertedToLogId,omitempty"` // Added
}

// --- DTOs (Data Transfer Objects) for API/WebSocket ---

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

// WebSocket Messages
type WebSocketMessage struct {
	Action  string      `json:"action"`
	Payload interface{} `json:"payload"`
	Seq     int64       `json:"seq,omitempty"`
}

type AuthPayload struct {
	Token string `json:"token"`
}

// ... (LoginRequest, RegisterRequest, LoginResponse, WebSocketMessage, AuthPayload, ErrorPayload, ContentRequestPayload, ContentResponsePayload, IncrementalUpdatePayload, CreatePostPayload, CreateCodeFilePayload, DeleteItemPayload, SuccessPayload, ApplyChangesSuccessPayload remain same) ...

type ErrorPayload struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"` // Optional error code (e.g., "CONFLICT", "NOT_FOUND")
	Action  string `json:"action,omitempty"`
	Seq     int64  `json:"seq,omitempty"`
}

type ContentRequestPayload struct {
	ItemID   string `json:"itemId"`
	ItemType string `json:"itemType"` // Expect "post" or "codefile" string from client
}

type ContentResponsePayload struct {
	ItemID   string `json:"itemId"`
	ItemType string `json:"itemType"`
	Content  string `json:"content"`
	Version  int    `json:"version"` // Send current version to client
}

// REMOVED: UpdateContentPayload (replaced by IncrementalUpdatePayload)
// type UpdateContentPayload struct {
// 	ItemID   string `json:"itemId"`
// 	ItemType string `json:"itemType"`
// 	Content  string `json:"content"`
// }

// IncrementalUpdatePayload is used for the 'apply_changes' action
type IncrementalUpdatePayload struct {
	ItemID      string   `json:"itemId"`
	ItemType    string   `json:"itemType"`    // Expect "post" or "codefile" string
	BaseVersion int      `json:"baseVersion"` // The version the client based the changes on
	Changes     []Change `json:"changes"`     // List of changes in this update
}

type CreatePostPayload struct {
	Title          string `json:"title"`
	InitialContent string `json:"initialContent"`
}

type CreateCodeFilePayload struct {
	FileName       string `json:"fileName"`
	Language       string `json:"language"`
	InitialContent string `json:"initialContent"`
}

type DeleteItemPayload struct {
	ItemID   string `json:"itemId"`
	ItemType string `json:"itemType"` // Expect "post" or "codefile" string
}

type SuccessPayload struct {
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	Action  string      `json:"action,omitempty"`
	Seq     int64       `json:"seq,omitempty"`
}

// ApplyChangesSuccessPayload includes the new version after applying changes
type ApplyChangesSuccessPayload struct {
	ItemID     string `json:"itemId"`
	ItemType   string `json:"itemType"`
	NewVersion int    `json:"newVersion"`
	Message    string `json:"message"`
}

// --- New WebSocket Payloads ---

type SubscribePayload struct {
	ItemID   string `json:"itemId"`
	ItemType string `json:"itemType"`
}

type UnsubscribePayload struct {
	ItemID   string `json:"itemId"`
	ItemType string `json:"itemType"`
}

type GetHistoryPayload struct {
	ItemID   string `json:"itemId"`
	ItemType string `json:"itemType"`
	Limit    int    `json:"limit,omitempty"` // Optional limit
}

type RevertActionPayload struct {
	TargetLogID string `json:"targetLogId"` // The ID of the HistoryLog entry to revert TO
}

// BroadcastChangePayload is sent to subscribed clients when content changes
type BroadcastChangePayload struct {
	ItemID     string   `json:"itemId"`
	ItemType   string   `json:"itemType"`
	Changes    []Change `json:"changes"`    // The changes that were applied
	NewVersion int      `json:"newVersion"` // The version *after* changes
	Originator string   `json:"originator"` // UserID of the client who made the change (optional, for client-side logic)
}

// BroadcastDeletePayload is sent when an item is deleted
type BroadcastDeletePayload struct {
	ItemID   string `json:"itemId"`
	ItemType string `json:"itemType"`
}

// ItemType defines the type of content item (Post or CodeFile).
type ItemType string

const (
	// ItemTypeUnknown represents an unspecified or invalid item type.
	ItemTypeUnknown ItemType = "" // Or use "unknown"
	// ItemTypePost represents a blog post.
	ItemTypePost ItemType = "post"
	// ItemTypeCodeFile represents a code file.
	ItemTypeCodeFile ItemType = "codefile"
)

// IsValid checks if the ItemType is one of the recognized types.
func (it ItemType) IsValid() bool {
	switch it {
	case ItemTypePost, ItemTypeCodeFile:
		return true
	default:
		return false
	}
}

// User represents a user in the system
type User struct {
	ID           string    `json:"id" bson:"_id,omitempty" dynamodbav:"id" firestore:"-"`
	Username     string    `json:"username" bson:"username" dynamodbav:"username" firestore:"username"`
	PasswordHash string    `json:"-" bson:"passwordHash" dynamodbav:"passwordHash" firestore:"passwordHash"`
	CreatedAt    time.Time `json:"createdAt" bson:"createdAt" dynamodbav:"createdAt" firestore:"createdAt"`
}

// Post represents blog post metadata
type Post struct {
	ID        string    `json:"id" bson:"_id,omitempty" dynamodbav:"id" firestore:"-"`
	UserID    string    `json:"userId" bson:"userId" dynamodbav:"userId" firestore:"userId"`
	Title     string    `json:"title" bson:"title" dynamodbav:"title" firestore:"title"`
	Slug      string    `json:"slug" bson:"slug" dynamodbav:"slug" firestore:"slug"`
	CreatedAt time.Time `json:"createdAt" bson:"createdAt" dynamodbav:"createdAt" firestore:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt" bson:"updatedAt" dynamodbav:"updatedAt" firestore:"updatedAt"`
	S3Path    string    `json:"-" bson:"s3Path" dynamodbav:"s3Path" firestore:"s3Path"`
	Version   int       `json:"-" bson:"version" dynamodbav:"version" firestore:"version"` // For OCC
}

// CodeFile represents coding workspace file metadata
type CodeFile struct {
	ID        string    `json:"id" bson:"_id,omitempty" dynamodbav:"id" firestore:"-"`
	UserID    string    `json:"userId" bson:"userId" dynamodbav:"userId" firestore:"userId"`
	FileName  string    `json:"fileName" bson:"fileName" dynamodbav:"fileName" firestore:"fileName"`
	Language  string    `json:"language" bson:"language" dynamodbav:"language" firestore:"language"`
	CreatedAt time.Time `json:"createdAt" bson:"createdAt" dynamodbav:"createdAt" firestore:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt" bson:"updatedAt" dynamodbav:"updatedAt" firestore:"updatedAt"`
	S3Path    string    `json:"-" bson:"s3Path" dynamodbav:"s3Path" firestore:"s3Path"`
	Version   int       `json:"-" bson:"version" dynamodbav:"version" firestore:"version"` // For OCC
}

// Change represents a single modification within a file for incremental updates.go
type Change struct {
	Line    int    `json:"line"`    // 0-based line number where change starts
	Column  int    `json:"column"`  // 0-based column number where change starts
	Text    string `json:"text"`    // Text to insert
	Removed int    `json:"removed"` // Number of characters to remove *before* inserting text
}
