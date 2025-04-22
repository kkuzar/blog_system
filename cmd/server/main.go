package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/kkuzar/blog_system/internal/api"
	"github.com/kkuzar/blog_system/internal/auth"
	"github.com/kkuzar/blog_system/internal/cache"       // Added
	"github.com/kkuzar/blog_system/internal/cache/redis" // Added
	"github.com/kkuzar/blog_system/internal/config"
	"github.com/kkuzar/blog_system/internal/database"
	"github.com/kkuzar/blog_system/internal/middleware"
	"github.com/kkuzar/blog_system/internal/service"
	"github.com/kkuzar/blog_system/internal/storage"
	"github.com/kkuzar/blog_system/internal/websocket"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/kkuzar/blog_system/docs"
)

// ... (Swagger annotations remain same) ...

func main() {
	// --- Configuration ---
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// --- Initialize Components ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize JWT Auth
	auth.Init(&cfg.JWT)

	// Initialize Cache Adapter (Redis or NoOp)
	var cacheAdapter cache.Cache
	redisCache, err := redis.NewRedisCache(&cfg.Redis)
	if err == nil && redisCache != nil {
		cacheAdapter = redisCache
		defer func() {
			if err := cacheAdapter.Close(); err != nil {
				log.Printf("Error closing cache adapter: %v", err)
			}
		}()
		log.Println("Redis Cache Adapter initialized")
	} else {
		if err != nil && !errors.Is(err, errors.New("redis disabled")) { // Log actual errors
			log.Printf("WARNING: Failed to initialize Redis Cache, falling back to NoOpCache: %v", err)
		} else {
			log.Println("Redis disabled or not configured, using NoOpCache.")
		}
		cacheAdapter = cache.NewNoOpCache() // Use NoOp if Redis fails or is disabled
	}

	// Initialize Storage Adapter
	storageAdapter, err := storage.NewStorageAdapter(&cfg.Storage)
	// ... (handle error, defer close) ...
	log.Printf("Storage Adapter initialized (Type: %s)", cfg.Storage.Type)

	// Initialize Database Adapter
	dbAdapter, err := database.NewDBAdapter(ctx, &cfg.Database)
	// ... (handle error, defer close) ...
	log.Printf("Database Adapter initialized (Type: %s)", cfg.Database.Type)

	// Initialize Service Layer (Inject Cache and Config)
	appService := service.NewService(dbAdapter, storageAdapter, cacheAdapter, cfg)
	log.Println("Service Layer initialized")

	// Initialize WebSocket Hub
	wsHub := websocket.NewHub()
	go wsHub.Run()
	log.Println("WebSocket Hub initialized and running")

	// --- Setup HTTP Server ---
	mux := http.NewServeMux()
	api.SetupRoutes(mux, appService, wsHub) // Pass service and hub
	loggedMux := middleware.LoggingMiddleware(mux)
	serverAddr := fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.Server.Port)
	httpServer := &http.Server{
		Addr:    serverAddr,
		Handler: loggedMux,
		// ... (timeouts) ...
	}

	// --- Start Server & Graceful Shutdown ---
	// ... (ListenAndServe in goroutine, wait for signal, httpServer.Shutdown) ...

	log.Println("Application shut down complete.")
}
