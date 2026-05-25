package config

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/convoy/backend/internal/httpx"
)

// AppConfigStore is a tiny key-value store backed by the `app_config` table.
// Used for runtime-tunable knobs that the admin needs to change without a
// redeploy — currently `min_client_version`, but the API is intentionally
// generic so adding more knobs is a one-row insert.
type AppConfigStore struct {
	pool *pgxpool.Pool
}

func NewAppConfigStore(pool *pgxpool.Pool) *AppConfigStore {
	return &AppConfigStore{pool: pool}
}

// Get returns the raw string value for a key. ErrNoRows when missing — the
// caller is expected to handle that (most call sites supply a default).
func (s *AppConfigStore) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_config WHERE key = $1`, key).Scan(&v)
	return v, err
}

// Set upserts the key. updated_at is bumped automatically so the admin UI
// can show "last changed" if it wants to.
func (s *AppConfigStore) Set(ctx context.Context, key, value string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO app_config (key, value, updated_at)
		 VALUES ($1, $2, now())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		key, value,
	)
	return err
}

// --- Keys & defaults -------------------------------------------------------

const (
	// KeyMinClientVersion stores the lowest mobile-app version the backend
	// will accept. Format: semver "major.minor.patch". A client below this
	// gets an `UpdateRequired` screen on launch (delivered via /config).
	KeyMinClientVersion = "min_client_version"
)

// MinClientVersion convenience accessor. "0.0.0" is the fall-back when the
// row is missing (means "no floor", which is what the migration seeds).
func (s *AppConfigStore) MinClientVersion(ctx context.Context) string {
	v, err := s.Get(ctx, KeyMinClientVersion)
	if err != nil || strings.TrimSpace(v) == "" {
		return "0.0.0"
	}
	return strings.TrimSpace(v)
}

// --- Public /config endpoint -----------------------------------------------

// PublicConfig is the JSON shape the mobile app reads on launch. Kept lean:
// only what the client needs to decide whether to gate the UI or not.
type PublicConfig struct {
	MinClientVersion string         `json:"minClientVersion"`
	Entitlements     map[string]any `json:"entitlements"`
}

// Handler returns the public /config HTTP handler. No auth — the response
// is the same for everyone and the client needs it *before* sign-in.
func (s *AppConfigStore) Handler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	min := s.MinClientVersion(ctx)
	httpx.JSON(w, http.StatusOK, PublicConfig{
		MinClientVersion: min,
		// Empty for v1 — schema reserved for when paid tiers land. Free
		// tier is "no entitlements means all features".
		Entitlements: map[string]any{},
	})
}

// ErrNotFound mirrors pgx.ErrNoRows so the admin code can use a clean
// errors.Is check without importing pgx.
var ErrNotFound = errors.New("app_config: not found")

// IsNotFound returns true if the error chain contains pgx.ErrNoRows. Used by
// admin handlers so they don't have to import pgx directly.
func IsNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrNotFound)
}
