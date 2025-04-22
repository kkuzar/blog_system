// internal/websocket/hub.go
package websocket

import (
	"log"
	"sync"
)

// Hub maintains the set of active clients and broadcasts messages.
type Hub struct {
	// Registered clients. Maps client pointer to boolean (true if registered)
	clients map[*Client]bool

	// Inbound messages from the clients.
	broadcast chan []byte // Maybe broadcast structured messages instead?

	// Register requests from the clients.
	register chan *Client

	// Unregister requests from clients.
	unregister chan *Client

	// Mutex for thread-safe access to clients map when modifying outside run loop
	mu sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		broadcast:  make(chan []byte), // Consider buffering?
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
	}
}

// Run starts the hub's event loop in a separate goroutine.
func (h *Hub) Run() {
	log.Println("WebSocket Hub started")
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			log.Printf("Client registered: %s (Total: %d)", client.userID, len(h.clients))
			h.mu.Unlock()
		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send) // Close the send channel for this client
				log.Printf("Client unregistered: %s (Total: %d)", client.userID, len(h.clients))
			}
			h.mu.Unlock()
		case message := <-h.broadcast:
			// This broadcasts to ALL clients. Might need more targeted messaging.
			h.mu.RLock()
			log.Printf("Broadcasting message to %d clients", len(h.clients))
			for client := range h.clients {
				select {
				case client.send <- message:
					// Message sent successfully
				default:
					// Send buffer is full, client might be slow or disconnected.
					// Close the connection and unregister the client.
					log.Printf("Client %s send buffer full, closing connection.", client.userID)
					// Need to unregister outside the read lock
					go func(c *Client) { h.unregister <- c }(client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// GetClientCount returns the number of currently connected clients.
func (h *Hub) GetClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Add specific broadcast methods if needed, e.g., BroadcastToUser(userID string, message []byte)
