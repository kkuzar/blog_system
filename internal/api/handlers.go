// internal/api/handlers.go
package api

import (
	"encoding/json"
	"github.com/kkuzar/blog_system/internal/models"
	"github.com/kkuzar/blog_system/internal/service"
	"log"
	"net/http"
	"strconv"
	"strings"
	// "github.com/gorilla/mux" // If using mux for path variables
)

type APIHandler struct {
	service *service.Service
}

func NewAPIHandler(s *service.Service) *APIHandler {
	return &APIHandler{service: s}
}

// writeJSON is a helper to write JSON responses
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		if err := json.NewEncoder(w).Encode(data); err != nil {
			// Log error, but response header is already sent
			log.Printf("Error encoding JSON response: %v", err)
		}
	}
}

// writeError is a helper to write JSON error responses
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// Register godoc
// @Summary Register a new user
// @Description Creates a new user account.
// @Tags auth
// @Accept json
// @Produce json
// @Param user body models.RegisterRequest true "Registration Info"
// @Success 201 {object} models.User "User created successfully (excluding password hash)"
// @Failure 400 {object} map[string]string "Invalid input"
// @Failure 409 {object} map[string]string "Username already taken"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /auth/register [post]
func (h *APIHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req models.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	user, err := h.service.RegisterUser(r.Context(), req.Username, req.Password)
	if err != nil {
		if err == service.ErrUsernameTaken {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to register user")
		}
		return
	}

	writeJSON(w, http.StatusCreated, user)
}

// Login godoc
// @Summary Log in a user
// @Description Authenticates a user and returns a JWT token.
// @Tags auth
// @Accept json
// @Produce json
// @Param credentials body models.LoginRequest true "Login Credentials"
// @Success 200 {object} models.LoginResponse "Login successful"
// @Failure 400 {object} map[string]string "Invalid input"
// @Failure 401 {object} map[string]string "Invalid credentials"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /auth/login [post]
func (h *APIHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req models.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	token, user, err := h.service.LoginUser(r.Context(), req.Username, req.Password)
	if err != nil {
		if err == service.ErrInvalidCredentials {
			writeError(w, http.StatusUnauthorized, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "Login failed")
		}
		return
	}

	resp := models.LoginResponse{
		Token: token,
		User:  *user, // Return user info (without hash)
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- Read/List Handlers ---

// ListPosts godoc
// @Summary List posts metadata
// @Description Get a list of post metadata for a user (or public, depending on implementation). Requires authentication.
// @Tags posts
// @Produce json
// @Param userId query string true "User ID to list posts for" // Or get from context if listing own posts
// @Param limit query int false "Limit number of results" default(10)
// @Param offset query int false "Offset for pagination" default(0)
// @Security BearerAuth
// @Success 200 {array} models.Post "List of post metadata"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /posts [get]
func (h *APIHandler) ListPosts(w http.ResponseWriter, r *http.Request) {
	// Example: Get target UserID from query param.
	// Alternatively, get logged-in UserID from context if API should only list *own* posts.
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "userId query parameter is required")
		return
		// Or use logged-in user:
		// userID := middleware.GetUserIDFromContext(r.Context())
		// if userID == "" {
		//     writeError(w, http.StatusUnauthorized, "Authentication required") // Should be caught by middleware anyway
		//     return
		// }
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 10 // Default limit
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	posts, err := h.service.ListUserPosts(r.Context(), userID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list posts")
		return
	}

	writeJSON(w, http.StatusOK, posts)
}

// GetPost godoc
// @Summary Get post metadata by ID
// @Description Retrieves metadata for a single post. Requires authentication.
// @Tags posts
// @Produce json
// @Param id path string true "Post ID"
// @Security BearerAuth
// @Success 200 {object} models.Post "Post metadata"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 404 {object} map[string]string "Post not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /posts/{id} [get]
func (h *APIHandler) GetPost(w http.ResponseWriter, r *http.Request) {
	// Need a way to get path parameters. Standard library requires manual parsing or a router.
	// Example: Assuming path is /api/v1/posts/{id}
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 4 { // e.g., ["api", "v1", "posts", "{id}"]
		writeError(w, http.StatusBadRequest, "Invalid URL path")
		return
	}
	postID := pathParts[3]

	post, err := h.service.GetPostDetails(r.Context(), postID)
	if err != nil {
		if err == service.ErrItemNotFound {
			writeError(w, http.StatusNotFound, "Post not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to get post details")
		}
		return
	}

	// Optional: Add authorization check here if posts aren't public
	// loggedInUserID := middleware.GetUserIDFromContext(r.Context())
	// if post.UserID != loggedInUserID {
	//     writeError(w, http.StatusForbidden, "Access denied")
	//     return
	// }

	writeJSON(w, http.StatusOK, post)
}

// ListCodeFiles godoc
// @Summary List code files metadata
// @Description Get a list of code file metadata for a user. Requires authentication.
// @Tags codefiles
// @Produce json
// @Param userId query string true "User ID to list code files for"
// @Param limit query int false "Limit number of results" default(10)
// @Param offset query int false "Offset for pagination" default(0)
// @Security BearerAuth
// @Success 200 {array} models.CodeFile "List of code file metadata"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /code [get]
func (h *APIHandler) ListCodeFiles(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "userId query parameter is required")
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 10
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	files, err := h.service.ListUserCodeFiles(r.Context(), userID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list code files")
		return
	}

	writeJSON(w, http.StatusOK, files)
}

// GetCodeFile godoc
// @Summary Get code file metadata by ID
// @Description Retrieves metadata for a single code file. Requires authentication.
// @Tags codefiles
// @Produce json
// @Param id path string true "Code File ID"
// @Security BearerAuth
// @Success 200 {object} models.CodeFile "Code file metadata"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 404 {object} map[string]string "Code file not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /code/{id} [get]
func (h *APIHandler) GetCodeFile(w http.ResponseWriter, r *http.Request) {
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 4 { // e.g., ["api", "v1", "code", "{id}"]
		writeError(w, http.StatusBadRequest, "Invalid URL path")
		return
	}
	fileID := pathParts[3]

	file, err := h.service.GetCodeFileDetails(r.Context(), fileID)
	if err != nil {
		if err == service.ErrItemNotFound {
			writeError(w, http.StatusNotFound, "Code file not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to get code file details")
		}
		return
	}

	// Optional: Add authorization check
	// loggedInUserID := middleware.GetUserIDFromContext(r.Context())
	// if file.UserID != loggedInUserID {
	//     writeError(w, http.StatusForbidden, "Access denied")
	//     return
	// }

	writeJSON(w, http.StatusOK, file)
}
