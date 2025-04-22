// internal/middleware/logging.go
package middleware

import (
	"log"
	"net/http"
	"time"
)

func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("--> %s %s %s", r.Method, r.URL.Path, r.RemoteAddr)

		// Use a custom response writer to capture status code
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK} // Default to 200

		next.ServeHTTP(lrw, r)

		log.Printf("<-- %s %s %d %s", r.Method, r.URL.Path, lrw.statusCode, time.Since(start))
	})
}

// loggingResponseWriter wraps http.ResponseWriter to capture status code
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

// Optional: Override Write to capture size if needed
// func (lrw *loggingResponseWriter) Write(b []byte) (int, error) {
// 	// capture size
// 	return lrw.ResponseWriter.Write(b)
// }
