// Package web provides HTTP handlers for the web UI.
package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/jwtauth/v5"
)

//go:embed templates
var templateFS embed.FS

type contextKey string

const userRoleContextKey contextKey = "user_role"

// Handler renders web UI pages.
type Handler struct {
	templates *template.Template
}

// NewHandler creates a new web handler.
func NewHandler() (*Handler, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html", "templates/**/*.html")
	if err != nil {
		return nil, err
	}
	return &Handler{templates: tmpl}, nil
}

// PageData contains common template data.
type PageData struct {
	LoggedIn  bool
	UserRole  string
	PageTitle string
	Data      interface{}
}

// renderTemplate renders a template with the layout.
func (h *Handler) renderTemplate(w http.ResponseWriter, contentTemplate string, data PageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl, err := h.templates.Clone()
	if err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}

	contentAlias := fmt.Sprintf(`{{define "content"}}{{template %q .}}{{end}}`, contentTemplate)
	if _, err := tmpl.Parse(contentAlias); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}

	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
}

// LoginPage renders the login page.
func (h *Handler) LoginPage(w http.ResponseWriter, r *http.Request) {
	h.renderTemplate(w, "login_content", PageData{LoggedIn: false})
}

// SetupPage renders the first-run setup page.
func (h *Handler) SetupPage(w http.ResponseWriter, r *http.Request) {
	h.renderTemplate(w, "setup_content", PageData{LoggedIn: false})
}

// Dashboard renders the main dashboard.
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	h.renderTemplate(w, "dashboard_content", PageData{
		LoggedIn: true,
		UserRole: getUserRole(r),
	})
}

// BackendList renders the backends list page.
func (h *Handler) BackendList(w http.ResponseWriter, r *http.Request) {
	h.renderTemplate(w, "backends_list_content", PageData{
		LoggedIn:  true,
		UserRole:  getUserRole(r),
		PageTitle: "Backends",
	})
}

// BackendForm renders the backend create/edit form.
func (h *Handler) BackendForm(w http.ResponseWriter, r *http.Request) {
	h.renderTemplate(w, "backends_form_content", PageData{
		LoggedIn:  true,
		UserRole:  getUserRole(r),
		PageTitle: "Backend",
	})
}

// UserList renders the users list page.
func (h *Handler) UserList(w http.ResponseWriter, r *http.Request) {
	h.renderTemplate(w, "users_list_content", PageData{
		LoggedIn:  true,
		UserRole:  getUserRole(r),
		PageTitle: "Users",
	})
}

// getUserRole extracts the user role from context.
func getUserRole(r *http.Request) string {
	if role, ok := r.Context().Value(userRoleContextKey).(string); ok && role != "" {
		return role
	}
	return "user"
}

// StaticFS returns the embedded static files.
func StaticFS() (http.FileSystem, error) {
	sub, err := fs.Sub(templateFS, "templates")
	if err != nil {
		return nil, err
	}
	return http.FS(sub), nil
}

// AuthMiddleware validates JWT from Authorization header or cookie and sets user role.
func AuthMiddleware(jwtSecret string) func(http.Handler) http.Handler {
	tokenAuth := jwtauth.New("HS256", []byte(jwtSecret), nil)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := tokenFromRequest(r)
			if token == "" {
				http.Redirect(w, r, "/admin/login", http.StatusFound)
				return
			}

			jwtToken, err := jwtauth.VerifyToken(tokenAuth, token)
			if err != nil {
				http.Redirect(w, r, "/admin/login", http.StatusFound)
				return
			}

			claims, err := jwtToken.AsMap(r.Context())
			if err != nil {
				http.Redirect(w, r, "/admin/login", http.StatusFound)
				return
			}

			role, _ := claims["role"].(string)
			ctx := context.WithValue(r.Context(), userRoleContextKey, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AdminOnlyMiddleware ensures only admin users can access the page.
func AdminOnlyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if getUserRole(r) != "admin" {
			http.Error(w, "admin access required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func tokenFromRequest(r *http.Request) string {
	if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}
	if c, err := r.Cookie("jwt"); err == nil && c.Value != "" {
		return c.Value
	}
	if c, err := r.Cookie("token"); err == nil && c.Value != "" {
		// Backward compatibility with old cookie name.
		return c.Value
	}
	return ""
}
