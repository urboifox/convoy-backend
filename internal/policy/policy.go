// Package policy serves the user-facing privacy policy as a single HTML page
// (no markdown step, no static file hosting elsewhere). The mobile app deep
// links to /policy from its settings screen and from the Play Store listing.
package policy

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed templates/*.html
var templatesFS embed.FS

// Module renders the policy page. Constructed once at startup, parsed
// templates are reused on every request.
type Module struct {
	tpl *template.Template
	cfg Config
}

// Config holds the values rendered into the policy template. The mobile
// app reads ContactEmail too (via /config) so it can deep-link "email us
// to delete your account" without depending on this HTML page being live.
type Config struct {
	AppName       string // "Convoy"
	ContactEmail  string // user-facing support address
	EffectiveDate string // human-readable (e.g. "May 25, 2026")
}

// New parses the embedded template and validates required fields. Returns
// an error so main.go can degrade gracefully if email isn't set yet.
func New(cfg Config) (*Module, error) {
	if strings.TrimSpace(cfg.AppName) == "" {
		cfg.AppName = "Convoy"
	}
	if strings.TrimSpace(cfg.ContactEmail) == "" {
		return nil, fmt.Errorf("policy: ContactEmail is required (set PRIVACY_CONTACT_EMAIL)")
	}
	if strings.TrimSpace(cfg.EffectiveDate) == "" {
		cfg.EffectiveDate = "today"
	}
	sub, err := fs.Sub(templatesFS, "templates")
	if err != nil {
		return nil, err
	}
	tpl, err := template.New("privacy").ParseFS(sub, "privacy.html")
	if err != nil {
		return nil, err
	}
	return &Module{tpl: tpl, cfg: cfg}, nil
}

// Handler renders the policy page. Public, no auth — the Play Store
// reviewer needs to be able to GET it without credentials.
func (m *Module) Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Lenient cache: changes to the policy should fan out to readers
	// within an hour, balanced against not hammering the backend on every
	// app launch.
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if err := m.tpl.ExecuteTemplate(w, "privacy.html", m.cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
