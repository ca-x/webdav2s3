// Package server provides HTTP router setup.
package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/example/webdav-s3/ent"
	"github.com/example/webdav-s3/internal/api/handlers"
	"github.com/example/webdav-s3/internal/s3client"
	"github.com/example/webdav-s3/internal/web"
	davmount "github.com/example/webdav-s3/internal/webdav"
	"github.com/example/webdav-s3/pkg/auth"
	davadapter "github.com/example/webdav-s3/pkg/webdav"
	xwebdav "golang.org/x/net/webdav"
)

// SetupRouter creates the main HTTP router.
func SetupRouter(db *ent.Client, pool *s3client.Pool, jwtSecret string) (*chi.Mux, error) {
	r := chi.NewRouter()

	// Global middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.CleanPath)

	// Web UI handler
	webHandler, err := web.NewHandler()
	if err != nil {
		return nil, err
	}

	// API handler
	apiHandler := handlers.NewHandler(db, pool, jwtSecret)

	// Mount filesystem for multi-backend
	mountFs := davmount.NewMountFs(db, pool)

	// Load backends from database
	if err := mountFs.LoadBackends(nil); err != nil {
		// Log but don't fail - backends can be added later
	}

	// ── Health check ───────────────────────────────────────────────────────
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// ── Web UI routes ───────────────────────────────────────────────────────
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})

	r.Get("/admin/login", webHandler.LoginPage)

	r.Route("/admin", func(r chi.Router) {
		r.Use(web.AuthMiddleware)
		r.Get("/", webHandler.Dashboard)
		r.Get("/backends", webHandler.BackendList)
		r.Get("/backends/new", webHandler.BackendForm)
		r.Get("/backends/{id}", webHandler.BackendForm)
		r.Get("/users", webHandler.UserList)
	})

	// ── API routes ──────────────────────────────────────────────────────────
	r.Route("/api/v1", func(r chi.Router) {
		// Auth
		r.Post("/auth/login", apiHandler.Login)
		r.With(apiHandler.JWTMiddleware).Get("/auth/me", apiHandler.GetMe)

		// Backends (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(apiHandler.JWTMiddleware)
			r.Get("/backends", apiHandler.ListBackends)
			r.Post("/backends", apiHandler.CreateBackend)
			r.Get("/backends/{id}", apiHandler.GetBackend)
			r.Put("/backends/{id}", apiHandler.UpdateBackend)
			r.Delete("/backends/{id}", apiHandler.DeleteBackend)
			r.Post("/backends/{id}/test", apiHandler.TestBackend)
		})

		// Users (admin only)
		r.Group(func(r chi.Router) {
			r.Use(apiHandler.JWTMiddleware)
			r.Use(apiHandler.AdminOnly)
			r.Get("/users", apiHandler.ListUsers)
			r.Post("/users", apiHandler.CreateUser)
			r.Put("/users/{id}", apiHandler.UpdateUser)
			r.Delete("/users/{id}", apiHandler.DeleteUser)
		})
	})

	// ── WebDAV routes (with Basic auth) ─────────────────────────────────────
	// MountFs-based multi-backend WebDAV
	davHandler := &xwebdav.Handler{
		FileSystem: davadapter.NewAferoFS(mountFs),
		LockSystem: xwebdav.NewMemLS(),
	}

	r.HandleFunc("/mounts/*", func(w http.ResponseWriter, r *http.Request) {
		// Authenticate with database users
		username, password, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Validate against database
		dbAuth := auth.NewDatabase(db)
		valid, err := dbAuth.Authenticate(username, password)
		if err != nil || !valid {
			w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Strip /mounts prefix and let MountFs handle routing
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/mounts")
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
		davHandler.ServeHTTP(w, r)
	})

	return r, nil
}

// SetupLegacyRouter creates a router for legacy single-backend mode.
func SetupLegacyRouter(authenticator auth.Authenticator, fs *xwebdav.Handler, rateLimit int) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// WebDAV with basic auth
	r.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		// Wrap with auth and security middleware
		handler := authMiddleware(authenticator, fs)
		handler.ServeHTTP(w, r)
	})

	return r
}

func authMiddleware(authenticator auth.Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		valid, err := authenticator.Authenticate(username, password)
		if err != nil || !valid {
			w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}