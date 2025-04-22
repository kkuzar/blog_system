package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/kkuzar/blog_system/internal/auth"
	"github.com/kkuzar/blog_system/internal/cache"  // Added
	"github.com/kkuzar/blog_system/internal/config" // Added
	"github.com/kkuzar/blog_system/internal/database"
	"github.com/kkuzar/blog_system/internal/models"
	"github.com/kkuzar/blog_system/internal/storage"
	"github.com/kkuzar/blog_system/utils/pointer" // Added
	"io"
	"log"
	"strings"
	"sync" // Added for change counter
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// Service encapsulates business logic.
type Service struct {
	db      database.DBAdapter
	storage storage.StorageAdapter
	cache   cache.Cache    // Added
	cfg     *config.Config // Added
	// Track changes since last snapshot (in-memory, simple approach)
	// For multi-node, this needs distributed tracking (e.g., Redis counter)
	changeCounters map[string]int // Key: itemType:itemID
	counterMutex   sync.Mutex
}

// NewService creates a new service instance.
func NewService(db database.DBAdapter, storage storage.StorageAdapter, cache cache.Cache, cfg *config.Config) *Service {
	return &Service{
		db:             db,
		storage:        storage,
		cache:          cache, // Injected
		cfg:            cfg,   // Injected
		changeCounters: make(map[string]int),
		counterMutex:   sync.Mutex{},
	}
}

// --- Cache Keys (centralized, though could be in cache package) ---
const (
	userCacheDuration        = 1 * time.Hour
	itemMetaCacheDuration    = 30 * time.Minute
	itemContentCacheDuration = 10 * time.Minute // Shorter for content
)

// --- Error Definitions ---
var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUsernameTaken      = errors.New("username is already taken")
	ErrItemNotFound       = errors.New("item not found")
	ErrPermissionDenied   = errors.New("permission denied")
	ErrInvalidItemType    = errors.New("invalid item type specified")
	ErrVersionConflict    = errors.New("version conflict: item has been updated by another session")
	ErrApplyChange        = errors.New("failed to apply changes to content")
	ErrRevertNotAllowed   = errors.New("revert is only allowed for create or snapshot actions")
	ErrHistoryLogNotFound = errors.New("target history log entry not found")
	ErrInconsistentState  = errors.New("critical inconsistency detected") // For DB/S3 issues
)

// --- User Methods (with Caching) ---

func (s *Service) RegisterUser(ctx context.Context, username, password string) (*models.User, error) {
	// ... (hashing logic) ...
	user := &models.User{
		ID:           username,
		Username:     username,
		PasswordHash: string(hashedPassword),
		CreatedAt:    time.Now().UTC(),
	}

	err = s.db.CreateUser(ctx, user)
	// ... (error handling: ErrDuplicateUser -> ErrUsernameTaken) ...

	// Cache the new user (optional, as login will cache)
	// s.cache.SetUser(ctx, user, userCacheDuration) // Be careful caching before hash is cleared

	user.PasswordHash = "" // Clear hash before returning/caching
	return user, nil
}

func (s *Service) LoginUser(ctx context.Context, username, password string) (string, *models.User, error) {
	// 1. Check Cache
	cachedUser, err := s.cache.GetUser(ctx, username)
	if err == nil && cachedUser != nil {
		// Still need to fetch from DB for password hash comparison
		// log.Printf("User %s meta found in cache, fetching full from DB for auth", username)
	} else if err != nil && !errors.Is(err, cache.ErrNotFound) {
		log.Printf("Cache error fetching user %s: %v", username, err)
		// Proceed to DB, but log the cache error
	}

	// 2. Fetch from DB
	user, err := s.db.GetUserByUsername(ctx, username)
	if err != nil {
		// ... (map DB error to ErrInvalidCredentials) ...
	}

	// 3. Compare Password
	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	// ... (handle bcrypt error -> ErrInvalidCredentials) ...

	// 4. Generate JWT
	token, err := auth.GenerateJWT(user.ID)
	// ... (handle JWT error) ...

	// 5. Cache User (without hash)
	user.PasswordHash = ""
	if cacheErr := s.cache.SetUser(ctx, user, userCacheDuration); cacheErr != nil {
		log.Printf("Failed to cache user %s after login: %v", username, cacheErr)
	}

	return token, user, nil
}

// --- Read/List Methods (with Caching) ---

func (s *Service) getItemMetaWithCache(ctx context.Context, itemID string, itemType models.ItemType) (interface{}, error) {
	// 1. Check Cache
	cachedMeta, err := s.cache.GetItemMeta(ctx, itemID, itemType)
	if err == nil && cachedMeta != nil {
		// log.Printf("Item meta %s (%s) found in cache", itemID, itemType)
		return cachedMeta, nil
	}
	if err != nil && !errors.Is(err, cache.ErrNotFound) {
		log.Printf("Cache error fetching item meta %s (%s): %v", itemID, itemType, err)
	}

	// 2. Fetch from DB
	var dbMeta interface{}
	var dbErr error
	switch itemType {
	case models.ItemTypePost:
		dbMeta, dbErr = s.db.GetPostMetaByID(ctx, itemID)
	case models.ItemTypeCodeFile:
		dbMeta, dbErr = s.db.GetCodeFileMetaByID(ctx, itemID)
	default:
		return nil, ErrInvalidItemType
	}

	if dbErr != nil {
		return nil, mapDBError(dbErr, itemType, itemID) // mapDBError handles ErrNotFound
	}

	// 3. Set Cache
	if cacheErr := s.cache.SetItemMeta(ctx, itemID, itemType, dbMeta, itemMetaCacheDuration); cacheErr != nil {
		log.Printf("Failed to cache item meta %s (%s): %v", itemID, itemType, cacheErr)
	}

	return dbMeta, nil
}

func (s *Service) GetPostDetails(ctx context.Context, postID string) (*models.Post, error) {
	meta, err := s.getItemMetaWithCache(ctx, postID, models.ItemTypePost)
	if err != nil {
		return nil, err // Already mapped by getItemMetaWithCache
	}
	post, ok := meta.(*models.Post)
	if !ok {
		log.Printf("ERROR: Invalid type returned from cache/DB for post %s", postID)
		_ = s.cache.DeleteItemMeta(ctx, postID, models.ItemTypePost) // Clear bad cache entry
		return nil, errors.New("internal error retrieving post details")
	}
	return post, nil
}

func (s *Service) GetCodeFileDetails(ctx context.Context, fileID string) (*models.CodeFile, error) {
	meta, err := s.getItemMetaWithCache(ctx, fileID, models.ItemTypeCodeFile)
	if err != nil {
		return nil, err
	}
	file, ok := meta.(*models.CodeFile)
	if !ok {
		log.Printf("ERROR: Invalid type returned from cache/DB for codefile %s", fileID)
		_ = s.cache.DeleteItemMeta(ctx, fileID, models.ItemTypeCodeFile) // Clear bad cache entry
		return nil, errors.New("internal error retrieving code file details")
	}
	return file, nil
}

// List methods generally don't benefit as much from simple caching unless results are static
// or complex cache invalidation is implemented. Skipping cache for List... for now.
func (s *Service) ListUserPosts(ctx context.Context, userID string, limit, offset int) ([]models.Post, error) {
	// ... (fetch directly from DB) ...
}
func (s *Service) ListUserCodeFiles(ctx context.Context, userID string, limit, offset int) ([]models.CodeFile, error) {
	// ... (fetch directly from DB) ...
}

// --- Content Methods (with Caching) ---

func (s *Service) GetItemContent(ctx context.Context, userID, itemID string, itemTypeStr string) (content string, version int, err error) {
	itemType := models.ItemType(itemTypeStr)
	if !itemType.IsValid() {
		return "", 0, ErrInvalidItemType
	}

	// 1. Get Metadata (checks ownership, gets current version)
	meta, err := s.getItemMetaWithCache(ctx, itemID, itemType)
	if err != nil {
		return "", 0, err // Already mapped
	}

	var s3Path string
	var ownerUserID string
	var currentVersion int

	switch itemType {
	case models.ItemTypePost:
		postMeta := meta.(*models.Post)
		if postMeta.UserID != userID {
			return "", 0, ErrPermissionDenied
		}
		s3Path = postMeta.S3Path
		ownerUserID = postMeta.UserID
		currentVersion = postMeta.Version
	case models.ItemTypeCodeFile:
		fileMeta := meta.(*models.CodeFile)
		if fileMeta.UserID != userID {
			return "", 0, ErrPermissionDenied
		}
		s3Path = fileMeta.S3Path
		ownerUserID = fileMeta.UserID
		currentVersion = fileMeta.Version
	}
	// Ownership check again just in case
	if ownerUserID != userID {
		return "", 0, ErrPermissionDenied
	}

	// 2. Check Content Cache
	cachedContent, err := s.cache.GetItemContent(ctx, itemID, itemType, currentVersion)
	if err == nil {
		// log.Printf("Content cache hit for %s %s v%d", itemType, itemID, currentVersion)
		return cachedContent, currentVersion, nil
	}
	if err != nil && !errors.Is(err, cache.ErrNotFound) {
		log.Printf("Cache error fetching item content %s (%s) v%d: %v", itemID, itemType, currentVersion, err)
	}

	// 3. Fetch from S3 if not cached
	if s3Path == "" {
		// log.Printf("Item %s (%s) has no S3 path.", itemID, itemType)
		return "", currentVersion, nil // No content, return current version
	}

	reader, err := s.storage.DownloadFile(ctx, s3Path)
	if err != nil {
		if errors.Is(err, storage.ErrFileNotFound) {
			log.Printf("S3 file not found for path %s (item %s, type %s)", s3Path, itemID, itemType)
			return "", currentVersion, nil // Content missing, return current version
		}
		log.Printf("Error downloading file %s from storage: %v", s3Path, err)
		return "", 0, errors.New("failed to retrieve content")
	}
	defer reader.Close()

	contentBytes, err := io.ReadAll(reader)
	if err != nil {
		log.Printf("Error reading content stream for %s: %v", s3Path, err)
		return "", 0, errors.New("failed to read content")
	}
	content = string(contentBytes)

	// 4. Set Content Cache
	if cacheErr := s.cache.SetItemContent(ctx, itemID, itemType, currentVersion, content, itemContentCacheDuration); cacheErr != nil {
		log.Printf("Failed to cache item content %s (%s) v%d: %v", itemID, itemType, currentVersion, cacheErr)
	}

	return content, currentVersion, nil
}

// ApplyItemChanges applies incremental changes with OCC, caching, and snapshotting.
func (s *Service) ApplyItemChanges(ctx context.Context, userID, itemID, itemTypeStr string, baseVersion int, changes []models.Change) (newVersion int, appliedChanges []models.Change, err error) {
	itemType := models.ItemType(itemTypeStr)
	if !itemType.IsValid() {
		return 0, nil, ErrInvalidItemType
	}

	// 1. Get Metadata (checks ownership & base version via cache/DB)
	meta, err := s.getItemMetaWithCache(ctx, itemID, itemType)
	if err != nil {
		return 0, nil, err // Already mapped
	}

	var s3Path string
	var ownerUserID string
	var currentVersion int
	var contentType string

	switch itemType {
	case models.ItemTypePost:
		postMeta := meta.(*models.Post)
		if postMeta.UserID != userID {
			return 0, nil, ErrPermissionDenied
		}
		if postMeta.Version != baseVersion {
			log.Printf("Version conflict for %s %s: Client base %d, DB current %d", itemType, itemID, baseVersion, postMeta.Version)
			return postMeta.Version, nil, ErrVersionConflict
		}
		s3Path = postMeta.S3Path
		ownerUserID = postMeta.UserID
		currentVersion = postMeta.Version
		contentType = "text/markdown"
	case models.ItemTypeCodeFile:
		fileMeta := meta.(*models.CodeFile)
		if fileMeta.UserID != userID {
			return 0, nil, ErrPermissionDenied
		}
		if fileMeta.Version != baseVersion {
			log.Printf("Version conflict for %s %s: Client base %d, DB current %d", itemType, itemID, baseVersion, fileMeta.Version)
			return fileMeta.Version, nil, ErrVersionConflict
		}
		s3Path = fileMeta.S3Path
		ownerUserID = fileMeta.UserID
		currentVersion = fileMeta.Version
		contentType = "text/plain"
	}
	if ownerUserID != userID {
		return 0, nil, ErrPermissionDenied
	} // Redundant check

	// 2. Generate S3 Path if missing
	if s3Path == "" {
		s3Path = generateS3Path(userID, itemID, itemType)
		log.Printf("Generated S3 path for item %s (%s): %s", itemID, itemType, s3Path)
	}

	// 3. Download Current Content (Check cache first)
	currentContent, err := s.getItemContentFromSource(ctx, itemID, itemType, currentVersion, s3Path)
	if err != nil {
		log.Printf("Failed to get current content for patching %s %s v%d: %v", itemType, itemID, currentVersion, err)
		return 0, nil, fmt.Errorf("failed to retrieve current content for update: %w", err)
	}

	// 4. Apply Changes
	newContent, applyErr := applyChanges(currentContent, changes)
	if applyErr != nil {
		log.Printf("Error applying changes to %s %s: %v", itemType, itemID, applyErr)
		return 0, nil, ErrApplyChange
	}

	// --- Transaction-like block: S3 Upload -> DB Update ---
	// This order minimizes inconsistency if DB points to S3.

	// 5. Upload Patched Content to S3 *FIRST*
	uploadErr := s.storage.UploadFile(ctx, s3Path, strings.NewReader(newContent), contentType)
	if uploadErr != nil {
		log.Printf("Error uploading content to storage for %s (%s) at path %s: %v", itemID, itemType, s3Path, uploadErr)
		// Don't proceed to DB update if S3 fails
		return 0, nil, errors.New("failed to save updated content to storage")
	}

	// 6. Attempt to Update Metadata in DB (Atomic Version Increment)
	now := time.Now().UTC()
	expectedNewVersion := currentVersion + 1
	var dbUpdateErr error

	switch itemType {
	case models.ItemTypePost:
		postMeta := meta.(*models.Post)
		postMeta.UpdatedAt = now
		postMeta.Version = currentVersion                // Expected version for DB check
		postMeta.S3Path = s3Path                         // Ensure path is updated if generated
		dbUpdateErr = s.db.UpdatePostMeta(ctx, postMeta) // DB adapter increments version
	case models.ItemTypeCodeFile:
		fileMeta := meta.(*models.CodeFile)
		fileMeta.UpdatedAt = now
		fileMeta.Version = currentVersion                    // Expected version for DB check
		fileMeta.S3Path = s3Path                             // Ensure path is updated if generated
		dbUpdateErr = s.db.UpdateCodeFileMeta(ctx, fileMeta) // DB adapter increments version
	}

	if dbUpdateErr != nil {
		// S3 succeeded, but DB failed! Inconsistent state.
		log.Printf("CRITICAL INCONSISTENCY: S3 upload succeeded for %s %s path %s, but DB update failed: %v. Expected version %d.", itemType, itemID, s3Path, dbUpdateErr, currentVersion)

		// Attempt to fetch the actual current version if it was a version mismatch
		if errors.Is(dbUpdateErr, database.ErrVersionMismatch) {
			latestVersion, fetchErr := s.getItemVersion(ctx, itemID, itemType)
			if fetchErr != nil {
				log.Printf("Failed to fetch latest version after DB conflict for %s %s: %v", itemType, itemID, fetchErr)
				return 0, nil, ErrInconsistentState // Return generic inconsistency error
			}
			// Return the actual latest version and the conflict error
			return latestVersion, nil, ErrVersionConflict
		}
		// For other DB errors, return a generic inconsistency error
		return 0, nil, ErrInconsistentState
	}

	// --- Post-Update Actions (Cache, History, Snapshot) ---

	// 7. Invalidate/Update Caches
	_ = s.cache.DeleteItemMeta(ctx, itemID, itemType)        // Invalidate meta cache
	_ = s.cache.InvalidateItemContent(ctx, itemID, itemType) // Invalidate all old content versions
	// Cache the new content immediately
	if cacheErr := s.cache.SetItemContent(ctx, itemID, itemType, expectedNewVersion, newContent, itemContentCacheDuration); cacheErr != nil {
		log.Printf("Failed to cache new item content %s (%s) v%d: %v", itemID, itemType, expectedNewVersion, cacheErr)
	}

	// 8. Log Action History (Patch)
	for _, change := range changes {
		changeLogData := change // Create copy
		historyLog := &models.HistoryLog{
			UserID: userID, ItemID: itemID, ItemType: itemTypeStr,
			Action: models.ActionPatch, Timestamp: now,
			ChangeData: &changeLogData, ItemVersion: expectedNewVersion,
		}
		_, logErr := s.db.LogAction(ctx, historyLog)
		if logErr != nil {
			log.Printf("WARNING: Failed to log history patch for %s %s: %v", itemID, itemType, logErr)
		}
	}

	// 9. Snapshot Logic
	s.handleSnapshotting(ctx, userID, itemID, itemType, itemTypeStr, expectedNewVersion, s3Path, len(changes))

	return expectedNewVersion, changes, nil // Return applied changes for broadcast
}

// Helper to get content, checking cache first, then S3
func (s *Service) getItemContentFromSource(ctx context.Context, itemID string, itemType models.ItemType, version int, s3Path string) (string, error) {
	// Check cache
	cachedContent, err := s.cache.GetItemContent(ctx, itemID, itemType, version)
	if err == nil {
		return cachedContent, nil
	}
	if err != nil && !errors.Is(err, cache.ErrNotFound) {
		log.Printf("Cache error fetching content %s %s v%d: %v", itemType, itemID, version, err)
		// Fall through to S3
	}

	// Fetch from S3
	if s3Path == "" {
		return "", nil
	} // No path, no content

	reader, err := s.storage.DownloadFile(ctx, s3Path)
	if err != nil {
		if errors.Is(err, storage.ErrFileNotFound) {
			return "", nil
		} // Not found is ok
		return "", fmt.Errorf("s3 download error: %w", err)
	}
	defer reader.Close()
	contentBytes, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("s3 read error: %w", err)
	}

	// Don't cache here, cache after successful update in ApplyItemChanges

	return string(contentBytes), nil
}

// handleSnapshotting checks if a snapshot is needed and logs it.
func (s *Service) handleSnapshotting(ctx context.Context, userID, itemID string, itemType models.ItemType, itemTypeStr string, currentVersion int, currentS3Path string, numChangesApplied int) {
	interval := s.cfg.Snapshot.IntervalChanges
	if interval <= 0 {
		return // Snapshotting disabled
	}

	counterKey := fmt.Sprintf("%s:%s", itemType, itemID)

	s.counterMutex.Lock()
	s.changeCounters[counterKey] += numChangesApplied
	count := s.changeCounters[counterKey]
	if count >= interval {
		s.changeCounters[counterKey] = 0 // Reset counter
		s.counterMutex.Unlock()          // Unlock before logging

		// Log the snapshot action
		log.Printf("Creating snapshot for %s %s at version %d (change count %d >= %d)", itemType, itemID, currentVersion, count, interval)
		snapshotLog := &models.HistoryLog{
			UserID: userID, ItemID: itemID, ItemType: itemTypeStr,
			Action:      models.ActionSnapshot,
			Timestamp:   time.Now().UTC(), // Use current time for snapshot log
			S3PathAfter: currentS3Path,    // Log the S3 path *at the time of snapshot*
			ItemVersion: currentVersion,
		}
		_, logErr := s.db.LogAction(ctx, snapshotLog)
		if logErr != nil {
			log.Printf("WARNING: Failed to log snapshot action for %s %s: %v", itemID, itemType, logErr)
			// Should we put the count back if logging fails? Maybe not, just log warning.
		}
	} else {
		s.counterMutex.Unlock()
	}
}

// --- Create/Delete Methods (with Caching Invalidation) ---

func (s *Service) CreatePost(ctx context.Context, userID, title, initialContent string) (*models.Post, error) {
	// ... (generate slug, ID, path, create Post struct with Version: 1) ...
	post := &models.Post{ /* ... */ Version: 1}

	// 1. Create Metadata in DB
	dbPostID, err := s.db.CreatePostMeta(ctx, post)
	// ... (handle error) ...
	post.ID = dbPostID

	// 2. Upload Initial Content to S3
	// ... (handle upload) ...

	// 3. Log Action History (Create)
	historyLog := &models.HistoryLog{ /* ... */ Action: models.ActionCreate, S3PathAfter: post.S3Path, ItemVersion: post.Version}
	_, logErr := s.db.LogAction(ctx, historyLog)
	// ... (handle log error) ...

	// 4. Cache Meta & Content (optional, Get will cache anyway)
	_ = s.cache.SetItemMeta(ctx, post.ID, models.ItemTypePost, post, itemMetaCacheDuration)
	if initialContent != "" {
		_ = s.cache.SetItemContent(ctx, post.ID, models.ItemTypePost, post.Version, initialContent, itemContentCacheDuration)
	}

	return post, nil
}

func (s *Service) CreateCodeFile(ctx context.Context, userID, fileName, language, initialContent string) (*models.CodeFile, error) {
	// Similar to CreatePost:
	// ... Create CodeFile struct with Version: 1 ...
	codeFile := &models.CodeFile{ /* ... */ Version: 1}
	// ... Create Meta in DB ...
	// ... Upload Initial Content ...
	// ... Log ActionHistory (Create) ...
	// ... Cache Meta & Content ...
	return codeFile, nil
}

func (s *Service) DeleteItem(ctx context.Context, userID, itemID, itemTypeStr string) error {
	itemType := models.ItemType(itemTypeStr)
	if !itemType.IsValid() {
		return ErrInvalidItemType
	}

	// 1. Get Metadata (for s3path, version, ownership check)
	meta, err := s.getItemMetaWithCache(ctx, itemID, itemType) // Use cache
	if err != nil {
		if errors.Is(err, ErrItemNotFound) {
			return nil
		} // Already deleted? OK.
		return err
	}

	var s3Path string
	var ownerUserID string
	var currentVersion int

	switch itemType {
	case models.ItemTypePost:
		postMeta := meta.(*models.Post)
		if postMeta.UserID != userID {
			return ErrPermissionDenied
		}
		s3Path = postMeta.S3Path
		ownerUserID = postMeta.UserID
		currentVersion = postMeta.Version
	case models.ItemTypeCodeFile:
		fileMeta := meta.(*models.CodeFile)
		if fileMeta.UserID != userID {
			return ErrPermissionDenied
		}
		s3Path = fileMeta.S3Path
		ownerUserID = fileMeta.UserID
		currentVersion = fileMeta.Version
	}
	if ownerUserID != userID {
		return ErrPermissionDenied
	}

	// 2. Delete Metadata from DB
	switch itemType {
	case models.ItemTypePost:
		err = s.db.DeletePostMeta(ctx, itemID)
	case models.ItemTypeCodeFile:
		err = s.db.DeleteCodeFileMeta(ctx, itemID)
	}
	if err != nil { /* ... handle error ... */
	}

	// 3. Delete Content from S3
	if s3Path != "" {
		err = s.storage.DeleteFile(ctx, s3Path)
		// ... Log warning on error ...
	}

	// 4. Log Action History (Delete)
	historyLog := &models.HistoryLog{ /* ... */ Action: models.ActionDelete, S3PathBefore: s3Path, ItemVersion: currentVersion}
	_, logErr := s.db.LogAction(ctx, historyLog)
	// ... handle log error ...

	// 5. Invalidate Caches
	_ = s.cache.DeleteItemMeta(ctx, itemID, itemType)
	_ = s.cache.InvalidateItemContent(ctx, itemID, itemType) // Clear all content versions

	// Reset change counter for deleted item
	s.counterMutex.Lock()
	delete(s.changeCounters, fmt.Sprintf("%s:%s", itemType, itemID))
	s.counterMutex.Unlock()

	return nil
}

// --- History & Revert ---

func (s *Service) GetHistory(ctx context.Context, userID, itemID, itemTypeStr string, limit int) ([]models.HistoryLog, error) {
	itemType := models.ItemType(itemTypeStr)
	if !itemType.IsValid() {
		return nil, ErrInvalidItemType
	}

	// 1. Verify user owns the item (optional but good practice)
	meta, err := s.getItemMetaWithCache(ctx, itemID, itemType)
	if err != nil {
		return nil, err
	} // Includes not found check
	switch itemType {
	case models.ItemTypePost:
		if meta.(*models.Post).UserID != userID {
			return nil, ErrPermissionDenied
		}
	case models.ItemTypeCodeFile:
		if meta.(*models.CodeFile).UserID != userID {
			return nil, ErrPermissionDenied
		}
	}

	// 2. Fetch history from DB
	history, err := s.db.GetActionHistory(ctx, itemID, itemTypeStr, limit)
	if err != nil {
		log.Printf("Error fetching history for %s %s: %v", itemType, itemID, err)
		return nil, errors.New("failed to retrieve history")
	}
	return history, nil
}

// RevertToAction reverts the item's content to the state *after* the targetLogID action.
// Limited to reverting to 'create' or 'snapshot' log entries for simplicity.
func (s *Service) RevertToAction(ctx context.Context, userID, targetLogID string) (newItemVersion int, err error) {
	// 1. Fetch the Target History Log Entry
	targetLog, err := s.db.GetHistoryLogByID(ctx, targetLogID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return 0, ErrHistoryLogNotFound
		}
		log.Printf("Error fetching target history log %s: %v", targetLogID, err)
		return 0, errors.New("failed to retrieve target history state")
	}

	// 2. Basic Validation
	itemType := models.ItemType(targetLog.ItemType)
	if !itemType.IsValid() {
		return 0, ErrInvalidItemType
	}
	// Check if revert is allowed for this action type
	if targetLog.Action != models.ActionCreate && targetLog.Action != models.ActionSnapshot {
		return 0, ErrRevertNotAllowed
	}
	// S3 path must exist for create/snapshot
	if targetLog.S3PathAfter == "" {
		log.Printf("Cannot revert to log %s: Action is %s but S3PathAfter is missing.", targetLogID, targetLog.Action)
		return 0, errors.New("cannot revert: target state content path missing")
	}

	// 3. Verify Ownership (User owns the item associated with the log)
	meta, err := s.getItemMetaWithCache(ctx, targetLog.ItemID, itemType)
	if err != nil {
		return 0, err
	} // Item must still exist to be reverted

	var currentS3Path string
	var ownerUserID string
	var currentVersion int
	var contentType string

	switch itemType {
	case models.ItemTypePost:
		postMeta := meta.(*models.Post)
		if postMeta.UserID != userID {
			return 0, ErrPermissionDenied
		}
		currentS3Path = postMeta.S3Path // Get the *current* S3 path to overwrite
		ownerUserID = postMeta.UserID
		currentVersion = postMeta.Version
		contentType = "text/markdown"
	case models.ItemTypeCodeFile:
		fileMeta := meta.(*models.CodeFile)
		if fileMeta.UserID != userID {
			return 0, ErrPermissionDenied
		}
		currentS3Path = fileMeta.S3Path
		ownerUserID = fileMeta.UserID
		currentVersion = fileMeta.Version
		contentType = "text/plain"
	}
	if ownerUserID != userID {
		return 0, ErrPermissionDenied
	}
	if currentS3Path == "" { // Should not happen if item exists
		log.Printf("ERROR: Item %s %s exists but has no current S3 path during revert.", itemType, targetLog.ItemID)
		return 0, errors.New("internal error: item missing storage path")
	}

	// 4. Fetch the Content from the Target State's S3 Path
	revertContentReader, err := s.storage.DownloadFile(ctx, targetLog.S3PathAfter)
	if err != nil {
		log.Printf("Error downloading revert content from %s (log %s): %v", targetLog.S3PathAfter, targetLogID, err)
		return 0, errors.New("failed to retrieve content for revert state")
	}
	defer revertContentReader.Close()
	revertContentBytes, err := io.ReadAll(revertContentReader)
	if err != nil {
		log.Printf("Error reading revert content stream from %s (log %s): %v", targetLog.S3PathAfter, targetLogID, err)
		return 0, errors.New("failed to read content for revert state")
	}
	revertContent := string(revertContentBytes)

	// --- Transaction-like: Upload Reverted Content -> Update DB Meta ---

	// 5. Upload Reverted Content to the *Current* S3 Path
	err = s.storage.UploadFile(ctx, currentS3Path, strings.NewReader(revertContent), contentType)
	if err != nil {
		log.Printf("Error uploading reverted content to %s for item %s %s: %v", currentS3Path, itemType, targetLog.ItemID, err)
		return 0, errors.New("failed to save reverted content")
	}

	// 6. Update Item Metadata (Increment version)
	now := time.Now().UTC()
	expectedNewVersion := currentVersion + 1
	var dbUpdateErr error

	switch itemType {
	case models.ItemTypePost:
		postMeta := meta.(*models.Post)
		postMeta.UpdatedAt = now
		postMeta.Version = currentVersion // Expected version for DB check
		dbUpdateErr = s.db.UpdatePostMeta(ctx, postMeta)
	case models.ItemTypeCodeFile:
		fileMeta := meta.(*models.CodeFile)
		fileMeta.UpdatedAt = now
		fileMeta.Version = currentVersion // Expected version for DB check
		dbUpdateErr = s.db.UpdateCodeFileMeta(ctx, fileMeta)
	}

	if dbUpdateErr != nil {
		log.Printf("CRITICAL INCONSISTENCY: S3 revert upload succeeded for %s %s path %s, but DB update failed: %v. Expected version %d.", itemType, targetLog.ItemID, currentS3Path, dbUpdateErr, currentVersion)
		// Don't return version conflict here, as it's a revert operation failure
		return 0, ErrInconsistentState
	}

	// 7. Invalidate Caches
	_ = s.cache.DeleteItemMeta(ctx, targetLog.ItemID, itemType)
	_ = s.cache.InvalidateItemContent(ctx, targetLog.ItemID, itemType)
	// Cache the reverted content
	_ = s.cache.SetItemContent(ctx, targetLog.ItemID, itemType, expectedNewVersion, revertContent, itemContentCacheDuration)

	// 8. Log the Revert Action
	revertLog := &models.HistoryLog{
		UserID: userID, ItemID: targetLog.ItemID, ItemType: targetLog.ItemType,
		Action:          models.ActionRevert,
		Timestamp:       now,
		S3PathAfter:     currentS3Path, // Path *after* the revert action
		ItemVersion:     expectedNewVersion,
		RevertedToLogID: pointer.To(targetLogID), // Link to the target log entry
	}
	_, logErr := s.db.LogAction(ctx, revertLog)
	if logErr != nil {
		log.Printf("WARNING: Failed to log revert action for %s %s: %v", targetLog.ItemID, itemType, logErr)
	}

	// Reset change counter after revert
	s.counterMutex.Lock()
	delete(s.changeCounters, fmt.Sprintf("%s:%s", itemType, targetLog.ItemID))
	s.counterMutex.Unlock()

	return expectedNewVersion, nil
}

// --- Helper Functions ---
// applyChanges, generateS3Path, mapDBError, getItemVersion remain similar
// generateSlug, TitleCase remain the same
