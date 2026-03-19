// Package handlers provides REST API handlers for the web UI.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/jwtauth/v5"

	"github.com/example/webdav-s3/ent"
	"github.com/example/webdav-s3/ent/s3backend"
	"github.com/example/webdav-s3/ent/user"
	"github.com/example/webdav-s3/internal/s3client"
	davmount "github.com/example/webdav-s3/internal/webdav"
	"github.com/example/webdav-s3/pkg/auth"
)

type Handler struct {
	db       *ent.Client
	pool     *s3client.Pool
	mountFs  *davmount.MountFs
	jwtAuth  *jwtauth.JWTAuth
	tokenTTL time.Duration
}

// NewHandler creates a new API handler.
func NewHandler(db *ent.Client, pool *s3client.Pool, mountFs *davmount.MountFs, jwtSecret string) *Handler {
	return &Handler{
		db:       db,
		pool:     pool,
		mountFs:  mountFs,
		jwtAuth:  jwtauth.New("HS256", []byte(jwtSecret), nil),
		tokenTTL: 24 * time.Hour,
	}
}

// ─────────────────────────────────────────────
// Auth
// ─────────────────────────────────────────────

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token     string   `json:"token"`
	ExpiresAt int64    `json:"expires_at"`
	User      UserInfo `json:"user"`
}

type UserInfo struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	initialized, err := h.db.User.Query().Exist(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to check setup status")
		return
	}
	if !initialized {
		writeJSONError(w, http.StatusConflict, "service not initialized, visit /admin/setup first")
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return
	}

	ctx := r.Context()
	dbAuth := auth.NewDatabase(h.db)

	valid, err := dbAuth.Authenticate(req.Username, req.Password)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !valid {
		writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	u, err := h.db.User.Query().
		Where(user.Username(req.Username)).
		Only(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "user not found")
		return
	}

	// Generate JWT
	expiresAt := time.Now().Add(h.tokenTTL)
	claims := map[string]interface{}{
		"user_id":  u.ID,
		"username": u.Username,
		"role":     u.Role.String(),
		"exp":      expiresAt.Unix(),
	}
	_, token, err := h.jwtAuth.Encode(claims)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "token generation failed")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "jwt",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})

	resp := LoginResponse{
		Token:     token,
		ExpiresAt: expiresAt.Unix(),
		User: UserInfo{
			ID:       u.ID,
			Username: u.Username,
			Role:     u.Role.String(),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	_, claims, _ := jwtauth.FromContext(r.Context())

	userID, ok := claims["user_id"].(float64)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()
	u, err := h.db.User.Get(ctx, int(userID))
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	resp := UserInfo{
		ID:       u.ID,
		Username: u.Username,
		Role:     u.Role.String(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type SetupStatusResponse struct {
	Initialized bool `json:"initialized"`
}

type SetupInitRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// GetSetupStatus returns whether at least one user exists.
func (h *Handler) GetSetupStatus(w http.ResponseWriter, r *http.Request) {
	initialized, err := h.db.User.Query().Exist(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to check setup status")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(SetupStatusResponse{Initialized: initialized})
}

// Initialize creates the first admin user when no users exist.
func (h *Handler) Initialize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	initialized, err := h.db.User.Query().Exist(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to check setup status")
		return
	}
	if initialized {
		writeJSONError(w, http.StatusConflict, "setup already completed")
		return
	}

	var req SetupInitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		writeJSONError(w, http.StatusBadRequest, "username is required")
		return
	}
	if len(req.Password) < 8 {
		writeJSONError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	u, err := h.db.User.Create().
		SetUsername(req.Username).
		SetPasswordHash(passwordHash).
		SetRole(user.RoleAdmin).
		SetIsEnabled(true).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			writeJSONError(w, http.StatusConflict, "username already exists")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "failed to create initial admin user")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(userToResponse(u))
}

// ─────────────────────────────────────────────
// Backends
// ─────────────────────────────────────────────

type BackendResponse struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Endpoint   string `json:"endpoint"`
	Region     string `json:"region"`
	Bucket     string `json:"bucket"`
	PathStyle  bool   `json:"path_style"`
	KeyPrefix  string `json:"key_prefix"`
	MountPath  string `json:"mount_path"`
	IsPrimary  bool   `json:"is_primary"`
	IsEnabled  bool   `json:"is_enabled"`
	IsReadonly bool   `json:"is_readonly"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func backendToResponse(b *ent.S3Backend) BackendResponse {
	return BackendResponse{
		ID:         b.ID,
		Name:       b.Name,
		Endpoint:   b.Endpoint,
		Region:     b.Region,
		Bucket:     b.Bucket,
		PathStyle:  b.PathStyle,
		KeyPrefix:  b.KeyPrefix,
		MountPath:  b.MountPath,
		IsPrimary:  b.IsPrimary,
		IsEnabled:  b.IsEnabled,
		IsReadonly: b.IsReadonly,
		CreatedAt:  b.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  b.UpdatedAt.Format(time.RFC3339),
	}
}

func (h *Handler) ListBackends(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	backends, err := h.db.S3Backend.Query().
		Order(ent.Asc(s3backend.FieldMountPath), ent.Desc(s3backend.FieldIsPrimary)).
		All(ctx)
	if err != nil {
		http.Error(w, "failed to list backends", http.StatusInternalServerError)
		return
	}

	resp := make([]BackendResponse, len(backends))
	for i, b := range backends {
		resp[i] = backendToResponse(b)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ListMountPaths returns unique mount paths with their backends
func (h *Handler) ListMountPaths(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	backends, err := h.db.S3Backend.Query().
		Where(s3backend.IsEnabledEQ(true)).
		Order(ent.Asc(s3backend.FieldMountPath), ent.Desc(s3backend.FieldIsPrimary)).
		All(ctx)
	if err != nil {
		http.Error(w, "failed to list backends", http.StatusInternalServerError)
		return
	}

	// Group by mount_path
	groups := make(map[string][]map[string]interface{})
	for _, b := range backends {
		groups[b.MountPath] = append(groups[b.MountPath], map[string]interface{}{
			"id":         b.ID,
			"name":       b.Name,
			"is_primary": b.IsPrimary,
			"bucket":     b.Bucket,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(groups)
}

type CreateBackendRequest struct {
	Name         string `json:"name"`
	Endpoint     string `json:"endpoint"`
	Region       string `json:"region"`
	Bucket       string `json:"bucket"`
	AccessKey    string `json:"access_key"`
	SecretKey    string `json:"secret_key"`
	SessionToken string `json:"session_token"`
	PathStyle    bool   `json:"path_style"`
	KeyPrefix    string `json:"key_prefix"`
	MountPath    string `json:"mount_path"`
	IsPrimary    bool   `json:"is_primary"`
	IsEnabled    bool   `json:"is_enabled"`
	IsReadonly   bool   `json:"is_readonly"`
}

func (h *Handler) CreateBackend(w http.ResponseWriter, r *http.Request) {
	var req CreateBackendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	mountPath, err := normalizeMountPath(req.MountPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	b, err := h.db.S3Backend.Create().
		SetName(req.Name).
		SetEndpoint(req.Endpoint).
		SetNillableRegion(&req.Region).
		SetBucket(req.Bucket).
		SetAccessKey(req.AccessKey).
		SetSecretKey(req.SecretKey).
		SetSessionToken(req.SessionToken).
		SetPathStyle(req.PathStyle).
		SetKeyPrefix(req.KeyPrefix).
		SetMountPath(mountPath).
		SetIsPrimary(req.IsPrimary).
		SetIsEnabled(req.IsEnabled).
		SetIsReadonly(req.IsReadonly).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			http.Error(w, "backend with this name already exists", http.StatusConflict)
			return
		}
		http.Error(w, "failed to create backend", http.StatusInternalServerError)
		return
	}
	if err := h.reloadMounts(ctx); err != nil {
		http.Error(w, "backend created but mount refresh failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(backendToResponse(b))
}

func (h *Handler) GetBackend(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	b, err := h.db.S3Backend.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			http.Error(w, "backend not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to get backend", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(backendToResponse(b))
}

type UpdateBackendRequest struct {
	Name         *string `json:"name"`
	Endpoint     *string `json:"endpoint"`
	Region       *string `json:"region"`
	Bucket       *string `json:"bucket"`
	AccessKey    *string `json:"access_key"`
	SecretKey    *string `json:"secret_key"`
	SessionToken *string `json:"session_token"`
	PathStyle    *bool   `json:"path_style"`
	KeyPrefix    *string `json:"key_prefix"`
	MountPath    *string `json:"mount_path"`
	IsPrimary    *bool   `json:"is_primary"`
	IsEnabled    *bool   `json:"is_enabled"`
	IsReadonly   *bool   `json:"is_readonly"`
}

func (h *Handler) UpdateBackend(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var req UpdateBackendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	update := h.db.S3Backend.UpdateOneID(id)

	if req.Name != nil {
		update = update.SetName(*req.Name)
	}
	if req.Endpoint != nil {
		update = update.SetEndpoint(*req.Endpoint)
	}
	if req.Region != nil {
		update = update.SetRegion(*req.Region)
	}
	if req.Bucket != nil {
		update = update.SetBucket(*req.Bucket)
	}
	if req.AccessKey != nil {
		update = update.SetAccessKey(*req.AccessKey)
	}
	if req.SecretKey != nil {
		update = update.SetSecretKey(*req.SecretKey)
	}
	if req.SessionToken != nil {
		update = update.SetSessionToken(*req.SessionToken)
	}
	if req.PathStyle != nil {
		update = update.SetPathStyle(*req.PathStyle)
	}
	if req.KeyPrefix != nil {
		update = update.SetKeyPrefix(*req.KeyPrefix)
	}
	if req.MountPath != nil {
		mountPath, err := normalizeMountPath(*req.MountPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		update = update.SetMountPath(mountPath)
	}
	if req.IsPrimary != nil {
		update = update.SetIsPrimary(*req.IsPrimary)
	}
	if req.IsEnabled != nil {
		update = update.SetIsEnabled(*req.IsEnabled)
	}
	if req.IsReadonly != nil {
		update = update.SetIsReadonly(*req.IsReadonly)
	}

	b, err := update.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			http.Error(w, "backend not found", http.StatusNotFound)
			return
		}
		if ent.IsConstraintError(err) {
			http.Error(w, "backend with this name already exists", http.StatusConflict)
			return
		}
		http.Error(w, "failed to update backend", http.StatusInternalServerError)
		return
	}

	// Invalidate cached client
	h.pool.Remove(id)
	if err := h.reloadMounts(ctx); err != nil {
		http.Error(w, "backend updated but mount refresh failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(backendToResponse(b))
}

func (h *Handler) DeleteBackend(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	err = h.db.S3Backend.DeleteOneID(id).Exec(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			http.Error(w, "backend not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to delete backend", http.StatusInternalServerError)
		return
	}

	h.pool.Remove(id)
	if err := h.reloadMounts(ctx); err != nil {
		http.Error(w, "backend deleted but mount refresh failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) TestBackend(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	b, err := h.db.S3Backend.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			http.Error(w, "backend not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to get backend", http.StatusInternalServerError)
		return
	}

	err = h.pool.TestConnection(ctx, b)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// ─────────────────────────────────────────────
// Users (admin only)
// ─────────────────────────────────────────────

type UserResponse struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	IsEnabled bool   `json:"is_enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func userToResponse(u *ent.User) UserResponse {
	return UserResponse{
		ID:        u.ID,
		Username:  u.Username,
		Role:      u.Role.String(),
		IsEnabled: u.IsEnabled,
		CreatedAt: u.CreatedAt.Format(time.RFC3339),
		UpdatedAt: u.UpdatedAt.Format(time.RFC3339),
	}
}

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	users, err := h.db.User.Query().All(ctx)
	if err != nil {
		http.Error(w, "failed to list users", http.StatusInternalServerError)
		return
	}

	resp := make([]UserResponse, len(users))
	for i, u := range users {
		resp[i] = userToResponse(u)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type CreateUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		http.Error(w, "failed to hash password", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	role := user.RoleUser
	if req.Role == "admin" {
		role = user.RoleAdmin
	}

	u, err := h.db.User.Create().
		SetUsername(req.Username).
		SetPasswordHash(passwordHash).
		SetRole(role).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			http.Error(w, "user with this username already exists", http.StatusConflict)
			return
		}
		http.Error(w, "failed to create user", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(userToResponse(u))
}

type UpdateUserRequest struct {
	Password  *string `json:"password"`
	Role      *string `json:"role"`
	IsEnabled *bool   `json:"is_enabled"`
}

func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var req UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	update := h.db.User.UpdateOneID(id)

	if req.Password != nil && *req.Password != "" {
		passwordHash, err := auth.HashPassword(*req.Password)
		if err != nil {
			http.Error(w, "failed to hash password", http.StatusInternalServerError)
			return
		}
		update = update.SetPasswordHash(passwordHash)
	}
	if req.Role != nil {
		role := user.RoleUser
		if *req.Role == "admin" {
			role = user.RoleAdmin
		}
		update = update.SetRole(role)
	}
	if req.IsEnabled != nil {
		update = update.SetIsEnabled(*req.IsEnabled)
	}

	u, err := update.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to update user", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(userToResponse(u))
}

func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	// Prevent deleting yourself
	_, claims, _ := jwtauth.FromContext(r.Context())
	if userID, ok := claims["user_id"].(float64); ok && int(userID) == id {
		http.Error(w, "cannot delete yourself", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	err = h.db.User.DeleteOneID(id).Exec(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to delete user", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ─────────────────────────────────────────────
// Middleware
// ─────────────────────────────────────────────

func (h *Handler) JWTMiddleware(next http.Handler) http.Handler {
	return jwtauth.Verifier(h.jwtAuth)(jwtauth.Authenticator(next))
}

func (h *Handler) AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, claims, _ := jwtauth.FromContext(r.Context())
		role, ok := claims["role"].(string)
		if !ok || role != "admin" {
			http.Error(w, "admin access required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) reloadMounts(ctx context.Context) error {
	if h.mountFs == nil {
		return nil
	}
	return h.mountFs.LoadBackends(ctx)
}

func normalizeMountPath(mountPath string) (string, error) {
	mountPath = strings.TrimSpace(mountPath)
	if mountPath == "" {
		return "", fmt.Errorf("mount_path is required")
	}
	if !strings.HasPrefix(mountPath, "/") {
		mountPath = "/" + mountPath
	}
	mountPath = path.Clean(mountPath)
	if mountPath == "." {
		mountPath = "/"
	}
	return mountPath, nil
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
