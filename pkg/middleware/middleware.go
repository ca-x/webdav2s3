// Package middleware provides HTTP middleware for the WebDAV server,
// mirroring pulsedav's security feature set.
package middleware

import (
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/example/webdav-s3/pkg/auth"
	"golang.org/x/time/rate"
)

// ─────────────────────────────────────────────
// Security headers
// ─────────────────────────────────────────────

// SecurityHeaders adds standard security response headers.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// ─────────────────────────────────────────────
// Request logger
// ─────────────────────────────────────────────

// Logger logs each incoming request with method, path, and remote addr.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rw, r)
		log.Printf("[%s] %s %s → %d (%s)",
			r.RemoteAddr, r.Method, r.URL.Path, rw.code, time.Since(start))
	})
}

type responseWriter struct {
	http.ResponseWriter
	code int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.code = code
	rw.ResponseWriter.WriteHeader(code)
}

// ─────────────────────────────────────────────
// Basic auth
// ─────────────────────────────────────────────

// BasicAuth enforces HTTP Basic Authentication using the given Authenticator.
func BasicAuth(authenticator auth.Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok {
			unauthorized(w)
			return
		}
		valid, err := authenticator.Authenticate(username, password)
		if err != nil {
			log.Printf("auth error: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		if !valid {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

// ─────────────────────────────────────────────
// Per-IP rate limiter (token bucket)
// ─────────────────────────────────────────────

type ipLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	rps      rate.Limit
	burst    int
}

var globalLimiter *ipLimiter

// RateLimit enforces a per-IP rate limit (requests per minute).
func RateLimit(requestsPerMinute int, next http.Handler) http.Handler {
	if requestsPerMinute <= 0 {
		return next
	}

	burst := requestsPerMinute / 10
	if burst < 1 {
		burst = 1
	}

	rps := rate.Limit(float64(requestsPerMinute) / 60.0)
	globalLimiter = &ipLimiter{
		limiters: make(map[string]*rate.Limiter),
		rps:      rps,
		burst:    burst,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		limiter := globalLimiter.get(ip)
		if !limiter.Allow() {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (il *ipLimiter) get(ip string) *rate.Limiter {
	il.mu.Lock()
	defer il.mu.Unlock()
	l, ok := il.limiters[ip]
	if !ok {
		l = rate.NewLimiter(il.rps, il.burst)
		il.limiters[ip] = l
	}
	return l
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		return ip[:idx]
	}
	return ip
}

// ─────────────────────────────────────────────
// File size limiter
// ─────────────────────────────────────────────

// MaxFileSize rejects PUT requests that exceed the given byte limit.
func MaxFileSize(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && maxBytes > 0 {
			if r.ContentLength > maxBytes {
				http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// ─────────────────────────────────────────────
// Path traversal protection
// ─────────────────────────────────────────────

// PathTraversalProtection rejects requests with suspicious path components.
func PathTraversalProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "..") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─────────────────────────────────────────────
// File extension filter
// ─────────────────────────────────────────────

// AllowedExtensions restricts PUT uploads to the given file extensions.
// Pass nil/empty to allow all extensions.
func AllowedExtensions(exts []string, next http.Handler) http.Handler {
	if len(exts) == 0 {
		return next
	}
	allowed := make(map[string]struct{}, len(exts))
	for _, e := range exts {
		allowed[strings.ToLower(e)] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			ext := strings.ToLower(filepath.Ext(r.URL.Path))
			if _, ok := allowed[ext]; !ok {
				http.Error(w, "Unsupported Media Type", http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ─────────────────────────────────────────────
// Read-only guard
// ─────────────────────────────────────────────

// ReadOnly blocks any method that would mutate the filesystem.
func ReadOnly(next http.Handler) http.Handler {
	mutatingMethods := map[string]bool{
		http.MethodPut:    true,
		http.MethodDelete: true,
		"MKCOL":           true,
		"MOVE":            true,
		"COPY":            true,
		"LOCK":            true,
		"UNLOCK":          true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mutatingMethods[r.Method] {
			http.Error(w, "Forbidden: read-only filesystem", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
