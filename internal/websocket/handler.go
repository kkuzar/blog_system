package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kkuzar/blog_system/internal/auth"
	"github.com/kkuzar/blog_system/internal/middleware"
	"github.com/kkuzar/blog_system/internal/models"
	"github.com/kkuzar/blog_system/internal/service"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// ... (upgrader, WebSocketHandler struct, NewWebSocketHandler, HandleConnections remain same) ...

// processMessage routes incoming messages.
func (h *WebSocketHandler) processMessage(client *Client, message []byte) {
	var msg models.WebSocketMessage
	// ... (unmarshal logic) ...

	// log.Printf("Received message: Action=%s, Authenticated=%v, UserID=%s, Seq=%d", msg.Action, client.isAuthenticated, client.userID, msg.Seq)

	// --- Authentication Handling ---
	if msg.Action == "auth" { /* ... handleAuth ... */
		return
	}

	// --- Authenticated Actions ---
	if !client.isAuthenticated { /* ... send auth required error ... */
		return
	}

	ctx := context.WithValue(context.Background(), middleware.UserIDContextKey, client.userID)

	switch msg.Action {
	case "get_content":
		h.handleGetContent(ctx, client, msg.Payload, msg.Seq)
	case "apply_changes":
		h.handleApplyChanges(ctx, client, msg.Payload, msg.Seq)
	case "create_post":
		h.handleCreatePost(ctx, client, msg.Payload, msg.Seq)
	case "create_codefile":
		h.handleCreateCodeFile(ctx, client, msg.Payload, msg.Seq)
	case "delete_item":
		h.handleDeleteItem(ctx, client, msg.Payload, msg.Seq)
	case "subscribe": // Added
		h.handleSubscribe(ctx, client, msg.Payload, msg.Seq)
	case "unsubscribe": // Added
		h.handleUnsubscribe(ctx, client, msg.Payload, msg.Seq)
	case "get_history": // Added
		h.handleGetHistory(ctx, client, msg.Payload, msg.Seq)
	case "revert_action": // Added
		h.handleRevertAction(ctx, client, msg.Payload, msg.Seq)
	default:
		// ... (send unknown action error) ...
	}
}

// --- Message Handler Implementations ---

// handleGetContent, handleCreatePost, handleCreateCodeFile remain similar (return data in SuccessPayload)

func (h *WebSocketHandler) handleApplyChanges(ctx context.Context, client *Client, payload interface{}, seq int64) {
	var req models.IncrementalUpdatePayload
	// ... (decode payload, validate itemType, check changes exist) ...
	itemType := models.ItemType(req.ItemType) // Get validated type

	userID := middleware.GetUserIDFromContext(ctx)
	newVersion, appliedChanges, err := h.service.ApplyItemChanges(ctx, userID, req.ItemID, req.ItemType, req.BaseVersion, req.Changes)
	if err != nil {
		// ... (handle service errors, including ErrVersionConflict) ...
		return
	}

	// Send success response to originator
	client.sendJSON(models.WebSocketMessage{
		Action: "changes_applied",
		Payload: models.ApplyChangesSuccessPayload{
			ItemID: req.ItemID, ItemType: req.ItemType, NewVersion: newVersion,
			Message: "Changes applied successfully",
		},
		Seq: seq,
	})

	// Broadcast the applied changes to other subscribers
	broadcastPayload := models.BroadcastChangePayload{
		ItemID:     req.ItemID,
		ItemType:   req.ItemType,
		Changes:    appliedChanges, // Send the actual changes
		NewVersion: newVersion,
		Originator: userID, // Let clients know who made the change
	}
	broadcastMsg := models.WebSocketMessage{
		Action:  "content_changed", // Specific action for broadcast
		Payload: broadcastPayload,
	}
	broadcastBytes, err := json.Marshal(broadcastMsg)
	if err != nil {
		log.Printf("ERROR: Failed to marshal broadcast message for %s %s: %v", itemType, req.ItemID, err)
		return
	}

	h.hub.broadcastToItem <- &ItemBroadcast{
		ItemID:     getItemSubKey(itemType, req.ItemID), // Use combined key
		Message:    broadcastBytes,
		Originator: client, // Pass the client object
	}
}

func (h *WebSocketHandler) handleDeleteItem(ctx context.Context, client *Client, payload interface{}, seq int64) {
	var req models.DeleteItemPayload
	// ... (decode payload, validate itemType) ...
	itemType := models.ItemType(req.ItemType)

	userID := middleware.GetUserIDFromContext(ctx)
	err := h.service.DeleteItem(ctx, userID, req.ItemID, req.ItemType)
	if err != nil {
		sendServiceError(client, err, "delete_item", seq)
		return
	}

	// Send success to originator
	client.sendJSON(models.WebSocketMessage{
		Action:  "delete_success",
		Payload: models.SuccessPayload{ /* ... */ },
		Seq:     seq,
	})

	// Broadcast deletion notification
	broadcastPayload := models.BroadcastDeletePayload{
		ItemID: req.ItemID, ItemType: req.ItemType,
	}
	broadcastMsg := models.WebSocketMessage{
		Action:  "item_deleted",
		Payload: broadcastPayload,
	}
	broadcastBytes, err := json.Marshal(broadcastMsg)
	if err != nil { /* ... log error ... */
		return
	}

	h.hub.broadcastToItem <- &ItemBroadcast{
		ItemID:     getItemSubKey(itemType, req.ItemID),
		Message:    broadcastBytes,
		Originator: client, // Can be nil if originator doesn't matter for delete broadcast
	}
}

// --- New Handlers ---

func (h *WebSocketHandler) handleSubscribe(ctx context.Context, client *Client, payload interface{}, seq int64) {
	var req models.SubscribePayload
	if !decodePayload(payload, &req, client, "subscribe", seq) {
		return
	}
	itemType := models.ItemType(req.ItemType)
	if !itemType.IsValid() { /* ... send error ... */
		return
	}

	// Optional: Verify user has permission to view/subscribe to this item
	// _, err := h.service.GetItemContent(ctx, client.userID, req.ItemID, req.ItemType) // Reuses permission check
	// if err != nil { sendServiceError(client, err, "subscribe", seq); return }

	subKey := getItemSubKey(itemType, req.ItemID)
	h.hub.subscribe <- &SubscriptionRequest{client: client, itemID: subKey}

	// Send confirmation back to client
	client.sendJSON(models.WebSocketMessage{
		Action:  "subscribe_success",
		Payload: map[string]string{"itemId": req.ItemID, "itemType": req.ItemType},
		Seq:     seq,
	})
}

func (h *WebSocketHandler) handleUnsubscribe(ctx context.Context, client *Client, payload interface{}, seq int64) {
	var req models.UnsubscribePayload
	if !decodePayload(payload, &req, client, "unsubscribe", seq) {
		return
	}
	itemType := models.ItemType(req.ItemType)
	if !itemType.IsValid() { /* ... send error ... */
		return
	}

	subKey := getItemSubKey(itemType, req.ItemID)
	h.hub.unsubscribe <- &SubscriptionRequest{client: client, itemID: subKey}

	// Send confirmation back to client
	client.sendJSON(models.WebSocketMessage{
		Action:  "unsubscribe_success",
		Payload: map[string]string{"itemId": req.ItemID, "itemType": req.ItemType},
		Seq:     seq,
	})
}

func (h *WebSocketHandler) handleGetHistory(ctx context.Context, client *Client, payload interface{}, seq int64) {
	var req models.GetHistoryPayload
	if !decodePayload(payload, &req, client, "get_history", seq) {
		return
	}
	itemType := models.ItemType(req.ItemType)
	if !itemType.IsValid() { /* ... send error ... */
		return
	}

	userID := middleware.GetUserIDFromContext(ctx)
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	} // Default limit

	history, err := h.service.GetHistory(ctx, userID, req.ItemID, req.ItemType, limit)
	if err != nil {
		sendServiceError(client, err, "get_history", seq)
		return
	}

	client.sendJSON(models.WebSocketMessage{
		Action:  "history_data",
		Payload: history, // Send array of history logs
		Seq:     seq,
	})
}

func (h *WebSocketHandler) handleRevertAction(ctx context.Context, client *Client, payload interface{}, seq int64) {
	var req models.RevertActionPayload
	if !decodePayload(payload, &req, client, "revert_action", seq) {
		return
	}
	if req.TargetLogID == "" { /* ... send error ... */
		return
	}

	userID := middleware.GetUserIDFromContext(ctx)
	newVersion, err := h.service.RevertToAction(ctx, userID, req.TargetLogID)
	if err != nil {
		sendServiceError(client, err, "revert_action", seq)
		return
	}

	// Fetch the latest content and metadata after revert to send back
	targetLog, _ := h.service.GetHistoryLogByID(ctx, req.TargetLogID) // Assume service checked ownership
	var itemID, itemTypeStr string
	if targetLog != nil {
		itemID = targetLog.ItemID
		itemTypeStr = targetLog.ItemType
	} else {
		// This shouldn't happen if revert succeeded, but handle defensively
		sendError(client, "Internal error after revert", "INTERNAL_ERROR", "revert_action", seq)
		return
	}

	// Send success with the new version
	client.sendJSON(models.WebSocketMessage{
		Action: "revert_success",
		Payload: map[string]interface{}{
			"message":    fmt.Sprintf("Successfully reverted item %s to state after log %s", itemID, req.TargetLogID),
			"itemId":     itemID,
			"itemType":   itemTypeStr,
			"newVersion": newVersion,
		},
		Seq: seq,
	})

	// TODO: Broadcast the revert? This is complex.
	// Other clients need the *full new content* and version.
	// Maybe trigger a special 'content_replaced' broadcast?
	// For now, other clients won't know about the revert until they refresh/resubscribe.
}

// --- Helper Functions (decodePayload, sendError, sendServiceError) remain similar ---
