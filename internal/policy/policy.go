// Package policy serves the user-facing legal pages (privacy policy +
// dedicated account-deletion landing page) as standalone HTML, embedded at
// build time. The mobile app deep links to /policy from its settings
// screen; Play Console's "account deletion" field points to
// /delete-account.
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

// Module renders the policy + deletion pages. Constructed once at startup,
// parsed templates are reused on every request.
type Module struct {
	tpl *template.Template
	cfg Config
}

// Config holds the values rendered into every legal-page template. The
// mobile app reads ContactEmail too (via /config) so it can deep-link
// "email us to delete your account" without depending on this HTML page
// being live.
type Config struct {
	AppName       string // "Convoy"
	ContactEmail  string // user-facing support address
	EffectiveDate string // human-readable (e.g. "May 25, 2026")
}

// New parses the embedded templates and validates required fields. Returns
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
	// Parse every *.html in the templates folder up-front so adding a new
	// legal page later is just a new file + new handler — no New()
	// changes.
	tpl, err := template.New("policy").ParseFS(sub, "*.html")
	if err != nil {
		return nil, err
	}
	return &Module{tpl: tpl, cfg: cfg}, nil
}

// PolicyHandler renders the privacy policy page. Public, no auth — the
// Play Store reviewer needs to be able to GET it without credentials.
func (m *Module) PolicyHandler(w http.ResponseWriter, _ *http.Request) {
	m.render(w, "privacy.html")
}

// DeleteAccountHandler renders the dedicated account-deletion landing
// page. Play Store requires a URL whose entire purpose is documenting how
// to delete the account — the privacy-policy anchor isn't enough.
func (m *Module) DeleteAccountHandler(w http.ResponseWriter, _ *http.Request) {
	m.render(w, "delete_account.html")
}

func (m *Module) render(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Lenient cache: changes should fan out to readers within an hour,
	// balanced against not hammering the backend on every app launch.
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if err := m.tpl.ExecuteTemplate(w, name, m.cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
