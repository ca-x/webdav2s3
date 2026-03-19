// Package server provides HTTP router setup.
package server

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/example/webdav-s3/ent"
	"github.com/example/webdav-s3/internal/api/handlers"
	"github.com/example/webdav-s3/internal/s3client"
	"github.com/example/webdav-s3/internal/web"
	davmount "github.com/example/webdav-s3/internal/webdav"
	"github.com/example/webdav-s3/pkg/auth"
	appmiddleware "github.com/example/webdav-s3/pkg/middleware"
	davadapter "github.com/example/webdav-s3/pkg/webdav"
	xwebdav "golang.org/x/net/webdav"
)

// SecurityConfig controls runtime security middleware settings.
type SecurityConfig struct {
	RateLimitPerMinute int
	MaxFileSizeBytes   int64
	AllowedExtensions  []string
	ReadOnly           bool
}

// SetupRouter creates the main HTTP router.
func SetupRouter(db *ent.Client, pool *s3client.Pool, jwtSecret string, securityCfg SecurityConfig) (*chi.Mux, error) {
	r := chi.NewRouter()

	// Global middleware
	r.Use(chimiddleware.RequestID)
	r.Use(requestIDResponseHeader)
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.CleanPath)

	// Web UI handler
	webHandler, err := web.NewHandler()
	if err != nil {
		return nil, err
	}

	// Mount filesystem for multi-backend
	mountFs := davmount.NewMountFs(db, pool)

	// API handler
	apiHandler := handlers.NewHandler(db, pool, mountFs, jwtSecret)

	// Load backends from database
	if err := mountFs.LoadBackends(context.Background()); err != nil {
		// Log but don't fail - backends can be added later
	}

	// ── Health check ───────────────────────────────────────────────────────
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// ── Web UI routes ───────────────────────────────────────────────────────
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})

	r.Get("/admin/login", func(w http.ResponseWriter, r *http.Request) {
		hasUsers, err := hasInitializedUsers(r.Context(), db)
		if err != nil {
			http.Error(w, "failed to check setup status", http.StatusInternalServerError)
			return
		}
		if !hasUsers {
			http.Redirect(w, r, "/admin/setup", http.StatusFound)
			return
		}
		webHandler.LoginPage(w, r)
	})
	r.Get("/admin/setup", func(w http.ResponseWriter, r *http.Request) {
		hasUsers, err := hasInitializedUsers(r.Context(), db)
		if err != nil {
			http.Error(w, "failed to check setup status", http.StatusInternalServerError)
			return
		}
		if hasUsers {
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}
		webHandler.SetupPage(w, r)
	})
	r.Get("/admin/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     "jwt",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			MaxAge:   -1,
		})
		http.Redirect(w, r, "/admin/login", http.StatusFound)
	})

	r.Route("/admin", func(r chi.Router) {
		r.Use(web.AuthMiddleware(jwtSecret))
		r.Get("/", webHandler.Dashboard)
		r.Get("/backends", webHandler.BackendList)
		r.Get("/backends/new", webHandler.BackendForm)
		r.Get("/backends/{id}", webHandler.BackendForm)
		r.With(web.AdminOnlyMiddleware).Get("/users", webHandler.UserList)
	})

	// ── API routes ──────────────────────────────────────────────────────────
	r.Route("/api/v1", func(r chi.Router) {
		// Setup (no auth, first user only)
		r.Get("/setup/status", apiHandler.GetSetupStatus)
		r.Post("/setup/init", apiHandler.Initialize)

		// Auth
		r.Post("/auth/login", apiHandler.Login)
		r.With(apiHandler.JWTMiddleware).Get("/auth/me", apiHandler.GetMe)

		// Backends (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(apiHandler.JWTMiddleware)
			r.Get("/backends", apiHandler.ListBackends)
			r.Get("/mount-paths", apiHandler.ListMountPaths)
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
		Logger: func(r *http.Request, err error) {
			if err == nil {
				return
			}
			log.Printf("request_id=%s webdav error: %s %s -> %v", requestID(r), r.Method, r.URL.Path, err)
		},
	}

	var mountHandler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Authenticate with database users
		username, password, ok := r.BasicAuth()
		if !ok {
			log.Printf("request_id=%s unauthorized webdav request: missing basic auth", requestID(r))
			w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Validate against database
		dbAuth := auth.NewDatabase(db)
		valid, err := dbAuth.Authenticate(username, password)
		if err != nil || !valid {
			log.Printf("request_id=%s unauthorized webdav request: invalid credentials user=%q err=%v", requestID(r), username, err)
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
	mountHandler = appmiddleware.PathTraversalProtection(mountHandler)
	mountHandler = appmiddleware.MaxFileSize(securityCfg.MaxFileSizeBytes, mountHandler)
	mountHandler = appmiddleware.AllowedExtensions(securityCfg.AllowedExtensions, mountHandler)
	if securityCfg.ReadOnly {
		mountHandler = appmiddleware.ReadOnly(mountHandler)
	}
	mountHandler = appmiddleware.RateLimit(securityCfg.RateLimitPerMinute, mountHandler)
	mountHandler = appmiddleware.SecurityHeaders(mountHandler)
	r.Handle("/mounts/*", mountHandler)

	return r, nil
}

// SetupLegacyRouter creates a router for legacy single-backend mode.
func SetupLegacyRouter(authenticator auth.Authenticator, fs *xwebdav.Handler, securityCfg SecurityConfig) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(requestIDResponseHeader)
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// WebDAV with basic auth
	handler := authMiddleware(authenticator, fs)
	handler = appmiddleware.PathTraversalProtection(handler)
	handler = appmiddleware.MaxFileSize(securityCfg.MaxFileSizeBytes, handler)
	handler = appmiddleware.AllowedExtensions(securityCfg.AllowedExtensions, handler)
	if securityCfg.ReadOnly {
		handler = appmiddleware.ReadOnly(handler)
	}
	handler = appmiddleware.RateLimit(securityCfg.RateLimitPerMinute, handler)
	handler = appmiddleware.SecurityHeaders(handler)
	r.Handle("/*", handler)

	return r
}

func authMiddleware(authenticator auth.Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok {
			log.Printf("request_id=%s unauthorized legacy request: missing basic auth", requestID(r))
			w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		valid, err := authenticator.Authenticate(username, password)
		if err != nil || !valid {
			log.Printf("request_id=%s unauthorized legacy request: invalid credentials user=%q err=%v", requestID(r), username, err)
			w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hasInitializedUsers(ctx context.Context, db *ent.Client) (bool, error) {
	return db.User.Query().Exist(ctx)
}

// SetHTTPLogger configures chi's default request logger output.
func SetHTTPLogger(logger *log.Logger) {
	if logger == nil {
		return
	}
	chimiddleware.DefaultLogger = chimiddleware.RequestLogger(&chimiddleware.DefaultLogFormatter{
		Logger:  logger,
		NoColor: true,
	})
}

func requestIDResponseHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reqID := requestID(r); reqID != "" {
			w.Header().Set(chimiddleware.RequestIDHeader, reqID)
		}
		next.ServeHTTP(w, r)
	})
}

func requestID(r *http.Request) string {
	return chimiddleware.GetReqID(r.Context())
}
