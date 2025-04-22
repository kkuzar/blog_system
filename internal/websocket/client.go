// internal/websocket/client.go
package websocket

import (
	"bytes"
	"encoding/json"
	"github.com/kkuzar/blog_system/internal/models"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 2048 * 1024 // 2MB limit for edits, adjust as needed
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

// Client is a middleman between the websocket connection and the hub.
type Client struct {
	hub *Hub

	// The websocket connection.
	conn *websocket.Conn

	// Buffered channel of outbound messages.
	send chan []byte

	// User ID associated with this client (set after successful auth)
	userID string

	// Is the client authenticated?
	isAuthenticated bool
}

// readPump pumps messages from the websocket connection to the hub's message processor.
func (c *Client) readPump(handler *WebSocketHandler) {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
		log.Printf("WebSocket readPump closed for client %s", c.userID)
	}()
	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait)) // Initial read deadline
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait)) // Reset read deadline on pong
		return nil
	})

	for {
		messageType, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket unexpected close error for client %s: %v", c.userID, err)
			} else {
				log.Printf("WebSocket read error for client %s: %v", c.userID, err)
			}
			break // Exit loop on error
		}

		// Reset read deadline on any message received
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))

		// We only process text messages containing JSON
		if messageType != websocket.TextMessage {
			log.Printf("Received non-text message type %d from client %s", messageType, c.userID)
			continue
		}

		message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))

		// Process the message using the handler
		handler.processMessage(c, message)
	}
}

// writePump pumps messages from the send channel to the websocket connection.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
		log.Printf("WebSocket writePump closed for client %s", c.userID)
		// No need to unregister here, readPump handles it on error/close
	}()
	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // Set deadline for this write
			if !ok {
				// The hub closed the channel.
				log.Printf("Client %s send channel closed by hub.", c.userID)
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				log.Printf("Error getting next writer for client %s: %v", c.userID, err)
				return // Exit loop on error
			}
			_, err = w.Write(message)
			if err != nil {
				log.Printf("Error writing message for client %s: %v", c.userID, err)
				// Don't return immediately, try closing the writer
			}

			// Add queued chat messages to the current websocket message.
			// This is an optimization for high-throughput scenarios, maybe not needed here.
			// n := len(c.send)
			// for i := 0; i < n; i++ {
			// 	w.Write(newline)
			// 	w.Write(<-c.send)
			// }

			if err := w.Close(); err != nil {
				log.Printf("Error closing writer for client %s: %v", c.userID, err)
				return // Exit loop on error
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("Error sending ping to client %s: %v", c.userID, err)
				return // Exit loop on error
			}
		}
	}
}

// sendJSON sends a structured message marshalled as JSON to the client.
func (c *Client) sendJSON(message interface{}) {
	if c == nil || c.send == nil {
		log.Println("Attempted to send JSON to nil client or closed channel")
		return
	}

	b, err := json.Marshal(message)
	if err != nil {
		log.Printf("Error marshalling JSON message for client %s: %v", c.userID, err)
		// Send an error message back to the client?
		errorMsg := models.WebSocketMessage{
			Action: "error",
			Payload: models.ErrorPayload{
				Message: "Internal server error: failed to serialize response",
			},
		}
		errorBytes, _ := json.Marshal(errorMsg)
		// Use non-blocking send with select to avoid deadlock if send channel is full
		select {
		case c.send <- errorBytes:
		default:
			log.Printf("Send channel full for client %s when trying to send serialization error.", c.userID)
			// Consider closing the connection here via unregister channel
			// go func() { c.hub.unregister <- c }()
		}
		return
	}

	// Use non-blocking send
	select {
	case c.send <- b:
		// Message queued successfully
	default:
		log.Printf("Send channel full for client %s. Dropping message: %s", c.userID, string(b))
		// Consider closing the connection here
		// go func() { c.hub.unregister <- c }()
	}
}
