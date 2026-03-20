package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "github.com/lib-x/entsqlite"

	"github.com/example/webdav-s3/ent"
	"github.com/example/webdav-s3/ent/user"
	"github.com/example/webdav-s3/internal/s3client"
	"github.com/example/webdav-s3/pkg/auth"
)

func newSetupTestHandler(t *testing.T) (*Handler, *ent.Client) {
	t.Helper()

	dsn := fmt.Sprintf("file:%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano())
	db, err := ent.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.Schema.Create(t.Context()); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	h := NewHandler(db, s3client.NewPool(), nil, "test-jwt-secret")
	return h, db
}

func TestInitializeCreatesFirstAdminUser(t *testing.T) {
	h, db := newSetupTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/init", strings.NewReader(`{"username":"root","password":"StrongPass123"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Initialize(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusCreated, rec.Code, rec.Body.String())
	}

	u, err := db.User.Query().Only(t.Context())
	if err != nil {
		t.Fatalf("query user: %v", err)
	}
	if u.Username != "root" {
		t.Fatalf("expected username root, got %s", u.Username)
	}
	if u.Role != user.RoleAdmin {
		t.Fatalf("expected role admin, got %s", u.Role)
	}
	if !u.IsEnabled {
		t.Fatal("expected initial user to be enabled")
	}
}

func TestInitializeRejectsWhenAlreadyInitialized(t *testing.T) {
	h, _ := newSetupTestHandler(t)

	firstReq := httptest.NewRequest(http.MethodPost, "/api/v1/setup/init", strings.NewReader(`{"username":"root","password":"StrongPass123"}`))
	firstReq.Header.Set("Content-Type", "application/json")
	firstRec := httptest.NewRecorder()
	h.Initialize(firstRec, firstReq)
	if firstRec.Code != http.StatusCreated {
		t.Fatalf("expected first init status %d, got %d", http.StatusCreated, firstRec.Code)
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/api/v1/setup/init", strings.NewReader(`{"username":"root2","password":"StrongPass123"}`))
	secondReq.Header.Set("Content-Type", "application/json")
	secondRec := httptest.NewRecorder()
	h.Initialize(secondRec, secondReq)

	if secondRec.Code != http.StatusConflict {
		t.Fatalf("expected second init status %d, got %d, body=%s", http.StatusConflict, secondRec.Code, secondRec.Body.String())
	}
}

func TestGetSetupStatus(t *testing.T) {
	h, db := newSetupTestHandler(t)

	checkStatus := func() SetupStatusResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
		rec := httptest.NewRecorder()
		h.GetSetupStatus(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
		}
		var resp SetupStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return resp
	}

	if resp := checkStatus(); resp.Initialized {
		t.Fatal("expected initialized=false before first user")
	}

	passwordHash, err := auth.HashPassword("StrongPass123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := db.User.Create().
		SetUsername("admin").
		SetPasswordHash(passwordHash).
		SetRole(user.RoleAdmin).
		SetIsEnabled(true).
		Save(t.Context()); err != nil {
		t.Fatalf("create user: %v", err)
	}

	if resp := checkStatus(); !resp.Initialized {
		t.Fatal("expected initialized=true after first user")
	}
}
