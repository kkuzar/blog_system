// internal/api/routes.go
package api

import (
	"github.com/kkuzar/blog_system/internal/middleware"
	"github.com/kkuzar/blog_system/internal/service"
	"github.com/kkuzar/blog_system/internal/websocket"
	"net/http"
	"strings"

	_ "github.com/kkuzar/blog_system/docs"       // Import generated docs
	httpSwagger "github.com/swaggo/http-swagger" // Import http-swagger
)

// SetupRoutes configures the HTTP routes using the standard library's ServeMux.
func SetupRoutes(mux *http.ServeMux, service *service.Service, wsHub *websocket.Hub) {
	apiHandler := NewAPIHandler(service)
	wsHandler := websocket.NewWebSocketHandler(service, wsHub)

	// Public routes (authentication)
	mux.HandleFunc("POST /api/v1/auth/register", apiHandler.Register)
	mux.HandleFunc("POST /api/v1/auth/login", apiHandler.Login)

	// WebSocket upgrade endpoint (authentication handled within the WS connection)
	mux.HandleFunc("/ws", wsHandler.HandleConnections)

	// --- Protected Routes (Read/List only via HTTP) ---
	// We need a simple way to group routes under middleware without a framework.
	// We can wrap the final handler function.

	// Posts API (Read/List)
	mux.HandleFunc("/api/v1/posts", func(w http.ResponseWriter, r *http.Request) {
		// Basic path matching for standard library mux
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/posts" {
			middleware.AuthMiddleware(apiHandler.ListPosts)(w, r)
		} else if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/posts/") {
			middleware.AuthMiddleware(apiHandler.GetPost)(w, r)
		} else {
			http.NotFound(w, r)
		}
	})
	// Handle trailing slash explicitly if needed, or rely on client not adding it
	mux.HandleFunc("/api/v1/posts/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/posts/") {
			// Ensure it's not the base path again which is handled above
			if len(strings.TrimPrefix(r.URL.Path, "/api/v1/posts/")) > 0 {
				middleware.AuthMiddleware(apiHandler.GetPost)(w, r)
			} else {
				http.NotFound(w, r) // Or redirect /posts/ -> /posts ?
			}
		} else {
			http.NotFound(w, r)
		}
	})

	// CodeFiles API (Read/List)
	mux.HandleFunc("/api/v1/code", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/code" {
			middleware.AuthMiddleware(apiHandler.ListCodeFiles)(w, r)
		} else if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/code/") {
			middleware.AuthMiddleware(apiHandler.GetCodeFile)(w, r)
		} else {
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/api/v1/code/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/code/") {
			if len(strings.TrimPrefix(r.URL.Path, "/api/v1/code/")) > 0 {
				middleware.AuthMiddleware(apiHandler.GetCodeFile)(w, r)
			} else {
				http.NotFound(w, r)
			}
		} else {
			http.NotFound(w, r)
		}
	})

	// Swagger UI endpoint
	// The URL needs to match the base path used in swagger annotations/config
	// Usually /swagger/index.html
	mux.HandleFunc("/swagger/", httpSwagger.WrapHandler)

}
