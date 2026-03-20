package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/webdav-s3/pkg/auth"
)

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()

	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	tests := []struct {
		header string
		want   string
	}{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"X-XSS-Protection", "1; mode=block"},
		{"Referrer-Policy", "strict-origin-when-cross-origin"},
	}

	for _, tt := range tests {
		if got := rec.Header().Get(tt.header); got != tt.want {
			t.Errorf("Header %q = %q, want %q", tt.header, got, tt.want)
		}
	}
}

type mockAuth struct {
	auth.Authenticator
}

func TestBasicAuth(t *testing.T) {
	t.Parallel()

	// Create a real local auth for testing
	validAuth := auth.NewLocal("admin", "secret")
	handler := BasicAuth(validAuth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name       string
		user       string
		pass       string
		wantStatus int
	}{
		{name: "valid credentials", user: "admin", pass: "secret", wantStatus: http.StatusOK},
		{name: "invalid password", user: "admin", pass: "wrong", wantStatus: http.StatusUnauthorized},
		{name: "invalid user", user: "user", pass: "secret", wantStatus: http.StatusUnauthorized},
		{name: "no auth header", user: "", pass: "", wantStatus: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.user != "" {
				req.SetBasicAuth(tt.user, tt.pass)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

type failingAuth struct{}

func (f *failingAuth) Authenticate(username, password string) (bool, error) {
	return false, errors.New("auth error")
}

func TestBasicAuthError(t *testing.T) {
	t.Parallel()

	errAuth := &failingAuth{}
	handler := BasicAuth(errAuth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called on auth error")
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestRateLimitZeroOrNegative(t *testing.T) {
	t.Parallel()

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	})

	handler := RateLimit(0, next)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !nextCalled {
		t.Error("next handler should be called when rate limit is 0")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRateLimitBurst(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := RateLimit(100, next)

	for i := range 10 {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "192.168.1.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("request %d: status = %d, want %d", i+1, rec.Code, http.StatusOK)
		}
	}
}

func TestClientIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		xff    string
		remote string
		want   string
	}{
		{name: "with X-Forwarded-For", xff: "203.0.113.1, 198.51.100.1", remote: "10.0.0.1:1234", want: "203.0.113.1"},
		{name: "single X-Forwarded-For", xff: "203.0.113.1", remote: "10.0.0.1:1234", want: "203.0.113.1"},
		{name: "no X-Forwarded-For", xff: "", remote: "192.168.1.1:5678", want: "192.168.1.1"},
		{name: "IPv6", xff: "", remote: "[::1]:8080", want: "[::1]"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = tt.remote
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}

			got := clientIP(req)
			if got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMaxFileSize(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := MaxFileSize(100, next)

	tests := []struct {
		name        string
		method      string
		contentLen  int64
		wantStatus  int
	}{
		{name: "small PUT", method: http.MethodPut, contentLen: 50, wantStatus: http.StatusOK},
		{name: "exact size PUT", method: http.MethodPut, contentLen: 100, wantStatus: http.StatusOK},
		{name: "large PUT", method: http.MethodPut, contentLen: 200, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "GET ignored", method: http.MethodGet, contentLen: 200, wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(tt.method, "/test", nil)
			req.ContentLength = tt.contentLen
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestPathTraversalProtection(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := PathTraversalProtection(next)

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "normal path", path: "/files/doc.txt", wantStatus: http.StatusOK},
		{name: "traversal attempt", path: "/files/../../etc/passwd", wantStatus: http.StatusForbidden},
		{name: "double dot", path: "/..%2F..%2Fetc/passwd", wantStatus: http.StatusForbidden},
		{name: "root path", path: "/", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestAllowedExtensions(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := AllowedExtensions([]string{".txt", ".pdf"}, next)

	tests := []struct {
		name       string
		path       string
		method     string
		wantStatus int
	}{
		{name: "allowed txt", path: "/file.txt", method: http.MethodPut, wantStatus: http.StatusOK},
		{name: "allowed pdf", path: "/doc.pdf", method: http.MethodPut, wantStatus: http.StatusOK},
		{name: "uppercase extension", path: "/doc.TXT", method: http.MethodPut, wantStatus: http.StatusOK},
		{name: "disallowed exe", path: "/file.exe", method: http.MethodPut, wantStatus: http.StatusUnsupportedMediaType},
		{name: "disallowed jpg", path: "/image.jpg", method: http.MethodPut, wantStatus: http.StatusUnsupportedMediaType},
		{name: "GET allowed", path: "/file.exe", method: http.MethodGet, wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestAllowedExtensionsEmpty(t *testing.T) {
	t.Parallel()

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := AllowedExtensions(nil, next)
	req := httptest.NewRequest(http.MethodPut, "/file.any", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !nextCalled {
		t.Error("next handler should be called when extensions is nil/empty")
	}
}

func TestReadOnly(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := ReadOnly(next)

	tests := []struct {
		name       string
		method     string
		wantStatus int
	}{
		{name: "GET allowed", method: http.MethodGet, wantStatus: http.StatusOK},
		{name: "HEAD allowed", method: http.MethodHead, wantStatus: http.StatusOK},
		{name: "OPTIONS allowed", method: http.MethodOptions, wantStatus: http.StatusOK},
		{name: "PUT blocked", method: http.MethodPut, wantStatus: http.StatusForbidden},
		{name: "DELETE blocked", method: http.MethodDelete, wantStatus: http.StatusForbidden},
		{name: "MKCOL blocked", method: "MKCOL", wantStatus: http.StatusForbidden},
		{name: "MOVE blocked", method: "MOVE", wantStatus: http.StatusForbidden},
		{name: "COPY blocked", method: "COPY", wantStatus: http.StatusForbidden},
		{name: "LOCK blocked", method: "LOCK", wantStatus: http.StatusForbidden},
		{name: "UNLOCK blocked", method: "UNLOCK", wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(tt.method, "/file", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("method %s: status = %d, want %d", tt.method, rec.Code, tt.wantStatus)
			}
		})
	}
}
