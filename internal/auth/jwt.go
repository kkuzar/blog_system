package auth

import (
	"errors"
	"fmt"
	"github.com/kkuzar/blog_system/internal/config"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken = errors.New("invalid or expired token")
	jwtSecret       []byte
	jwtExpiration   time.Duration
)

// Init initializes the JWT configuration. Call this once at startup.
func Init(cfg *config.JWTConfig) {
	if cfg.Secret == "" {
		panic("JWT Secret cannot be empty")
	}
	jwtSecret = []byte(cfg.Secret)
	jwtExpiration = cfg.Expiration
}

// GenerateJWT creates a new JWT token for a given user ID.
func GenerateJWT(userID string) (string, error) {
	if len(jwtSecret) == 0 {
		return "", errors.New("JWT secret not initialized")
	}

	claims := jwt.MapClaims{
		"sub": userID,                               // Subject (user ID)
		"iss": "go-blog-coder-backend",              // Issuer
		"iat": time.Now().Unix(),                    // Issued At
		"exp": time.Now().Add(jwtExpiration).Unix(), // Expiration Time
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(jwtSecret)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}
	return tokenString, nil
}

// ValidateJWT verifies a JWT token string and returns the user ID (subject).
func ValidateJWT(tokenString string) (string, error) {
	if len(jwtSecret) == 0 {
		return "", errors.New("JWT secret not initialized")
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		// Don't forget to validate the alg is what you expect:
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return jwtSecret, nil
	})

	if err != nil {
		// Handle specific errors like expiration
		if errors.Is(err, jwt.ErrTokenExpired) {
			return "", ErrInvalidToken
		}
		return "", fmt.Errorf("failed to parse token: %w", err)
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		userID, ok := claims["sub"].(string)
		if !ok || userID == "" {
			return "", ErrInvalidToken // Subject claim missing or not a string
		}
		// You could add more checks here (e.g., issuer)
		return userID, nil
	}

	return "", ErrInvalidToken
}
