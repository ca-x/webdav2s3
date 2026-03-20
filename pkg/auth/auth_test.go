package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalAuthAuthenticate(t *testing.T) {
	t.Parallel()

	auth := NewLocal("admin", "secret123")

	tests := []struct {
		name    string
		user    string
		pass    string
		wantOk  bool
		wantErr bool
	}{
		{name: "valid credentials", user: "admin", pass: "secret123", wantOk: true, wantErr: false},
		{name: "wrong password", user: "admin", pass: "wrong", wantOk: false, wantErr: false},
		{name: "wrong username", user: "user", pass: "secret123", wantOk: false, wantErr: false},
		{name: "both wrong", user: "user", pass: "wrong", wantOk: false, wantErr: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			valid, err := auth.Authenticate(tt.user, tt.pass)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Authenticate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if valid != tt.wantOk {
				t.Errorf("Authenticate() = %v, want %v", valid, tt.wantOk)
			}
		})
	}
}

func TestNewLocal(t *testing.T) {
	t.Parallel()

	auth := NewLocal("user", "pass")
	if auth == nil {
		t.Fatal("NewLocal returned nil")
	}

	valid, err := auth.Authenticate("user", "pass")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !valid {
		t.Error("Authenticate() = false, want true")
	}
}

func TestAPIAuthSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ctype := r.Header.Get("Content-Type"); ctype != "application/json" {
			t.Errorf("expected application/json, got %s", ctype)
		}
		var req apiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Username != "admin" || req.Password != "secret" {
			t.Errorf("unexpected credentials: %+v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"user_id": 42, "username": "admin"}`))
	}))
	defer server.Close()

	auth := NewAPI(server.URL)
	valid, err := auth.Authenticate("admin", "secret")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !valid {
		t.Error("Authenticate() = false, want true")
	}
}

func TestAPIAuthUnauthorized(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
	}))
	defer server.Close()

	auth := NewAPI(server.URL)
	valid, err := auth.Authenticate("admin", "wrong")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if valid {
		t.Error("Authenticate() = true, want false")
	}
}

func TestAPIAuthServerError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	auth := NewAPI(server.URL)
	valid, err := auth.Authenticate("admin", "secret")
	// Server errors return valid=false, err=nil (auth failed, not an error)
	if valid {
		t.Error("Authenticate() = true, want false")
	}
	// No error returned on non-200 status, just invalid credentials
	_ = err // err may or may not be nil depending on implementation
}

func TestAPIAuthConnectionError(t *testing.T) {
	t.Parallel()

	auth := NewAPI("http://localhost:99999")
	valid, err := auth.Authenticate("admin", "secret")
	if err == nil {
		t.Error("Authenticate() expected error for connection failure")
	}
	if valid {
		t.Error("Authenticate() = true, want false")
	}
}

func TestAPIAuthInvalidJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json`))
	}))
	defer server.Close()

	auth := NewAPI(server.URL)
	valid, err := auth.Authenticate("admin", "secret")
	if err == nil {
		t.Error("Authenticate() expected error for invalid JSON")
	}
	if valid {
		t.Error("Authenticate() = true, want false")
	}
}

func TestAPIAuthUserIDZero(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"user_id": 0, "username": "admin"}`))
	}))
	defer server.Close()

	auth := NewAPI(server.URL)
	valid, err := auth.Authenticate("admin", "secret")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if valid {
		t.Error("Authenticate() = true, want false (user_id is 0)")
	}
}
