package admin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	cfgstore "github.com/convoy/backend/internal/config"
	"github.com/convoy/backend/internal/feedback"
	"github.com/convoy/backend/internal/push"
)

//go:embed templates/*.html
var templatesFS embed.FS

// Config is everything Mount needs. Both Password and CookieSecret must be
// non-empty for New to succeed; otherwise the dashboard stays disabled.
type Config struct {
	Password     string
	CookieSecret []byte
	Pool         *pgxpool.Pool
	Feedback     *feedback.Store
	Push         *push.Store
	Expo         *push.ExpoClient
	// AppCfg powers the admin Settings page (edit min_client_version, future
	// feature flags). Required.
	AppCfg *cfgstore.AppConfigStore
}

// Module wires together templates, auth, and HTTP handlers.
type Module struct {
	cfg       Config
	pages     map[string]*template.Template
	store     *broadcastStore // implemented in broadcasts.go
}

const (
	cookieName       = "convoy_admin"
	sessionLifetime  = 7 * 24 * time.Hour
	sessionVersion   = "v1"
	loginRedirectURL = "/admin/"
)

// New validates the config and parses the embedded templates. Returns an
// error (not a panic) when disabled so main.go can degrade gracefully.
func New(cfg Config) (*Module, error) {
	if strings.TrimSpace(cfg.Password) == "" {
		return nil, errors.New("ADMIN_PASSWORD not set")
	}
	if len(cfg.CookieSecret) < 16 {
		return nil, errors.New("ADMIN_COOKIE_SECRET must be at least 16 bytes")
	}
	if cfg.Pool == nil {
		return nil, errors.New("admin: pool is required")
	}
	sub, err := fs.Sub(templatesFS, "templates")
	if err != nil {
		return nil, err
	}
	pages, err := parsePages(sub)
	if err != nil {
		return nil, err
	}
	return &Module{
		cfg:   cfg,
		pages: pages,
		store: &broadcastStore{pool: cfg.Pool},
	}, nil
}

// parsePages builds one template set per renderable page. Each "layout"
// page (dashboard, feedback, broadcasts, broadcast_new) is a clone of the
// shared layout with its own {{ define "content" }} pasted in. Standalone
// pages (login + partials) are parsed individually.
func parsePages(sub fs.FS) (map[string]*template.Template, error) {
	funcs := adminFuncs()

	layout, err := template.New("layout").Funcs(funcs).ParseFS(sub, "layout.html")
	if err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}

	pages := map[string]*template.Template{}
	withLayout := []string{
		"dashboard.html",
		"feedback.html",
		"broadcasts.html",
		"broadcast_new.html",
		"users.html",
		"user_detail.html",
		"settings.html",
	}
	for _, name := range withLayout {
		clone, err := layout.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := clone.ParseFS(sub, name); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		pages[name] = clone
	}

	standalone := []string{"login.html", "broadcast_result.html", "_recipient_count.html"}
	for _, name := range standalone {
		t, err := template.New(name).Funcs(funcs).ParseFS(sub, name)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		pages[name] = t
	}
	return pages, nil
}

func adminFuncs() template.FuncMap {
	return template.FuncMap{
		"formatTime": func(t time.Time) string {
			return t.Local().Format("Jan 2, 2006 · 15:04")
		},
		"initials": func(name string) string {
			name = strings.TrimSpace(name)
			if name == "" {
				return "?"
			}
			parts := strings.Fields(name)
			if len(parts) == 1 {
				return strings.ToUpper(parts[0][:1])
			}
			return strings.ToUpper(parts[0][:1] + parts[len(parts)-1][:1])
		},
	}
}

// Mount attaches the /admin routes to a chi router.
func (m *Module) Mount(r chi.Router) {
	r.Route("/admin", func(r chi.Router) {
		r.Get("/login", m.loginPage)
		r.Post("/login", m.loginSubmit)
		r.Post("/logout", m.logout)

		r.Group(func(r chi.Router) {
			r.Use(m.requireAuth)
			r.Get("/", m.dashboard)
			r.Get("/feedback", m.feedbackList)
			r.Get("/broadcasts", m.broadcastList)
			r.Get("/broadcasts/new", m.broadcastNewForm)
			r.Post("/broadcasts", m.broadcastSend)
			r.Get("/partials/recipient-count", m.recipientCount)

			r.Get("/users", m.usersList)
			r.Get("/users/{id}", m.userDetail)
			r.Post("/users/{id}/soft-delete", m.userSoftDelete)
			r.Post("/users/{id}/restore", m.userRestore)
			r.Post("/users/{id}/hard-delete", m.userHardDelete)

			r.Get("/settings", m.settingsPage)
			r.Post("/settings", m.settingsSubmit)
		})
	})
}

// --- session ---

// signSession produces a short opaque token: "<issued_at_unix>.<base64 hmac>".
// We only have to authenticate "this server signed it"; no user identity is
// embedded because there is exactly one admin.
func (m *Module) signSession(issuedAt int64) string {
	payload := fmt.Sprintf("%s.%d", sessionVersion, issuedAt)
	mac := hmac.New(sha256.New, m.cfg.CookieSecret)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig
}

func (m *Module) verifySession(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != sessionVersion {
		return false
	}
	payload := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, m.cfg.CookieSecret)
	mac.Write([]byte(payload))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(parts[2])) != 1 {
		return false
	}
	var issued int64
	if _, err := fmt.Sscanf(parts[1], "%d", &issued); err != nil {
		return false
	}
	if time.Since(time.Unix(issued, 0)) > sessionLifetime {
		return false
	}
	return true
}

func (m *Module) setSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    m.signSession(time.Now().Unix()),
		Path:     "/admin",
		HttpOnly: true,
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(sessionLifetime),
	})
}

func (m *Module) clearSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/admin",
		HttpOnly: true,
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func (m *Module) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || !m.verifySession(c.Value) {
			if isHTMX(r) {
				w.Header().Set("HX-Redirect", "/admin/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- login ---

func (m *Module) loginPage(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil && m.verifySession(c.Value) {
		http.Redirect(w, r, loginRedirectURL, http.StatusFound)
		return
	}
	m.render(w, "login.html", map[string]any{
		"Title": "Sign in",
		"Error": r.URL.Query().Get("err"),
	})
}

func (m *Module) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/admin/login?err=bad_request", http.StatusFound)
		return
	}
	pw := r.FormValue("password")
	if subtle.ConstantTimeCompare([]byte(pw), []byte(m.cfg.Password)) != 1 {
		// Use a generic message — timing is already constant.
		time.Sleep(250 * time.Millisecond)
		http.Redirect(w, r, "/admin/login?err=invalid", http.StatusFound)
		return
	}
	m.setSession(w, r)
	http.Redirect(w, r, loginRedirectURL, http.StatusFound)
}

func (m *Module) logout(w http.ResponseWriter, r *http.Request) {
	m.clearSession(w, r)
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

// --- dashboard / feedback ---

func (m *Module) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	feedbackCount, _ := m.cfg.Feedback.Count(ctx)
	tokenCount, _ := m.cfg.Push.Count(ctx)
	broadcastCount, _ := m.store.Count(ctx)
	recent, _ := m.cfg.Feedback.List(ctx, 5, nil)
	userCount, deletedCount := m.countUsers(ctx)

	m.render(w, "dashboard.html", map[string]any{
		"Title":          "Dashboard",
		"FeedbackCount":  feedbackCount,
		"TokenCount":     tokenCount,
		"BroadcastCount": broadcastCount,
		"UserCount":      userCount,
		"DeletedCount":   deletedCount,
		"RecentFeedback": recent,
		"NavActive":      "dashboard",
	})
}

// countUsers is a tiny helper for the dashboard tile. Returns (active,
// soft-deleted) so the overview can show both numbers without a second
// roundtrip.
func (m *Module) countUsers(ctx context.Context) (int, int) {
	var active, deleted int
	_ = m.cfg.Pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE deleted_at IS NULL),
		        count(*) FILTER (WHERE deleted_at IS NOT NULL)
		 FROM users`).Scan(&active, &deleted)
	return active, deleted
}

func (m *Module) feedbackList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	entries, err := m.cfg.Feedback.List(ctx, 200, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m.render(w, "feedback.html", map[string]any{
		"Title":     "Feedback",
		"Entries":   entries,
		"NavActive": "feedback",
	})
}

func (m *Module) recipientCount(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	n, _ := m.cfg.Push.Count(ctx)
	m.render(w, "_recipient_count.html", map[string]any{"Count": n})
}

// --- helpers ---

func (m *Module) render(w http.ResponseWriter, name string, data any) {
	t, ok := m.pages[name]
	if !ok {
		http.Error(w, "unknown template: "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// For layout-backed pages execute the layout entrypoint; for standalone
	// pages execute the page directly. Both forms are represented by the
	// same *template.Template via the clone-per-page strategy above.
	var err error
	if _, hasLayout := lookupLayout(t); hasLayout {
		err = t.ExecuteTemplate(w, "layout", data)
	} else {
		err = t.ExecuteTemplate(w, name, data)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func lookupLayout(t *template.Template) (*template.Template, bool) {
	if t == nil {
		return nil, false
	}
	if tt := t.Lookup("layout"); tt != nil {
		return tt, true
	}
	return nil, false
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}
