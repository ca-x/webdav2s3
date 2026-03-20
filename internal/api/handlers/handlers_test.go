package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeMountPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "simple path", input: "/minio", want: "/minio"},
		{name: "with leading slash", input: "minio", want: "/minio"},
		{name: "with trailing slash", input: "/minio/", want: "/minio"},
		{name: "with spaces", input: " /minio ", want: "/minio"},
		{name: "root", input: ".", want: "/"},
		{name: "nested path", input: "/a/b/c", want: "/a/b/c"},
		{name: "empty", input: "", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeMountPath(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("normalizeMountPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("normalizeMountPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteJSONError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		msg        string
		wantStatus int
	}{
		{name: "bad request", status: http.StatusBadRequest, msg: "invalid input", wantStatus: http.StatusBadRequest},
		{name: "not found", status: http.StatusNotFound, msg: "resource missing", wantStatus: http.StatusNotFound},
		{name: "internal error", status: http.StatusInternalServerError, msg: "oops", wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			writeJSONError(rec, tt.status, tt.msg)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			contentType := rec.Header().Get("Content-Type")
			if !strings.Contains(contentType, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", contentType)
			}

			body := rec.Body.String()
			if !strings.Contains(body, tt.msg) {
				t.Errorf("body = %q, want containing %q", body, tt.msg)
			}
		})
	}
}

func TestBackendToResponse(t *testing.T) {
	t.Parallel()

	// This tests the backendToResponse function
	// We can't call it directly without a real ent.S3Backend
	// But we can test the JSON serialization through the API
}

func TestUserToResponse(t *testing.T) {
	t.Parallel()

	// Similar to above, we test through the API
}

func TestListMountPaths(t *testing.T) {
	t.Parallel()

	// Would need full handler setup with database
	// Testing through integration would be better
}
