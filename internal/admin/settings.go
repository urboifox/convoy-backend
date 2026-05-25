package admin

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	cfgstore "github.com/convoy/backend/internal/config"
)

// settingsPage renders the runtime-config form. Currently a single field
// (minClientVersion), but new app_config keys will land here as the launch
// matures (e.g. feature flags).
func (m *Module) settingsPage(w http.ResponseWriter, r *http.Request) {
	if m.cfg.AppCfg == nil {
		http.Error(w, "app config store not configured", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	min := m.cfg.AppCfg.MinClientVersion(ctx)
	m.render(w, "settings.html", map[string]any{
		"Title":            "Settings",
		"NavActive":        "settings",
		"MinClientVersion": min,
		"Flash":            r.URL.Query().Get("flash"),
		"Err":              r.URL.Query().Get("err"),
	})
}

// semverRE is the format we accept for `min_client_version`. Strict because
// the client compares numerically — a typo here could either lock every
// user out (too high) or silently disable the gate (too low).
var semverRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// settingsSubmit handles the form post. Keys are whitelisted so the admin
// can't poke arbitrary rows.
func (m *Module) settingsSubmit(w http.ResponseWriter, r *http.Request) {
	if m.cfg.AppCfg == nil {
		http.Error(w, "app config store not configured", http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/admin/settings?err=bad_form", http.StatusFound)
		return
	}
	min := strings.TrimSpace(r.FormValue("min_client_version"))
	if !semverRE.MatchString(min) {
		http.Redirect(w, r, "/admin/settings?err=bad_version", http.StatusFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := m.cfg.AppCfg.Set(ctx, cfgstore.KeyMinClientVersion, min); err != nil {
		http.Redirect(w, r, "/admin/settings?err=db", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin/settings?flash=saved", http.StatusFound)
}
