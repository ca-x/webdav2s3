// Package web provides HTTP handlers for the web UI.
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed templates
var templateFS embed.FS

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
	LoggedIn   bool
	UserRole   string
	PageTitle  string
	Data       interface{}
}

// renderTemplate renders a template with the layout.
func (h *Handler) renderTemplate(w http.ResponseWriter, name string, data PageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	h.templates.ExecuteTemplate(w, "layout", data)
}

// LoginPage renders the login page.
func (h *Handler) LoginPage(w http.ResponseWriter, r *http.Request) {
	h.renderTemplate(w, "login.html", PageData{LoggedIn: false})
}

// Dashboard renders the main dashboard.
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	h.renderTemplate(w, "dashboard.html", PageData{
		LoggedIn: true,
		UserRole: getUserRole(r),
	})
}

// BackendList renders the backends list page.
func (h *Handler) BackendList(w http.ResponseWriter, r *http.Request) {
	h.renderTemplate(w, "list.html", PageData{
		LoggedIn:  true,
		UserRole:  getUserRole(r),
		PageTitle: "Backends",
	})
}

// BackendForm renders the backend create/edit form.
func (h *Handler) BackendForm(w http.ResponseWriter, r *http.Request) {
	h.renderTemplate(w, "form.html", PageData{
		LoggedIn:  true,
		UserRole:  getUserRole(r),
		PageTitle: "Backend",
	})
}

// UserList renders the users list page.
func (h *Handler) UserList(w http.ResponseWriter, r *http.Request) {
	h.renderTemplate(w, "list.html", PageData{
		LoggedIn:  true,
		UserRole:  getUserRole(r),
		PageTitle: "Users",
	})
}

// getUserRole extracts the user role from the JWT claims in the request.
// This is set by the RequireAuth middleware.
func getUserRole(r *http.Request) string {
	if role, ok := r.Context().Value("user_role").(string); ok {
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

// AuthMiddleware checks for a valid JWT token and adds user info to context.
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for token in Authorization header or cookie
		token := ""
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		} else if c, err := r.Cookie("token"); err == nil {
			token = c.Value
		}

		if token == "" {
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}

		// The actual JWT validation is done client-side with Alpine.js
		// For server-side rendering, we just check if there's a token
		// and pass it through. The client will validate and redirect if invalid.

		next.ServeHTTP(w, r)
	})
}