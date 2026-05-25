package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/convoy/backend/internal/auth/providers"
	"github.com/convoy/backend/internal/httpx"
)

type ctxKey struct{}

// Entitlements is the open-ended JSON blob attached to a user. Empty for free
// tier (current default); future paid features will populate keys like
// `voiceMinutesPerDay`, `maxStops`, etc., and the various services will read
// from here to gate features. Marshalled as `null`→`{}` for the client so
// the frontend never has to nil-check.
type Entitlements map[string]any

func (e Entitlements) MarshalJSON() ([]byte, error) {
	if e == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(map[string]any(e))
}

// Scan implements sql.Scanner so pgx can deserialise our JSONB column
// directly into the map without an intermediate []byte hop at every call
// site.
func (e *Entitlements) Scan(src any) error {
	if src == nil {
		*e = Entitlements{}
		return nil
	}
	var raw []byte
	switch v := src.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("entitlements: unsupported scan type %T", src)
	}
	if len(raw) == 0 {
		*e = Entitlements{}
		return nil
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("entitlements: %w", err)
	}
	*e = Entitlements(m)
	return nil
}

type User struct {
	ID           uuid.UUID    `json:"id"`
	DisplayName  string       `json:"displayName"`
	AvatarURL    *string      `json:"avatarUrl,omitempty"`
	Email        *string      `json:"email,omitempty"`
	Entitlements Entitlements `json:"entitlements"`
}

type Service struct {
	pool   *pgxpool.Pool
	secret []byte
	ttl    time.Duration

	// Optional OAuth verifiers. Either may be nil when the operator hasn't
	// configured client ids for that provider yet — the corresponding
	// HTTP handler then returns 503 so the mobile app can hide the
	// button instead of throwing on the network call.
	google *providers.GoogleVerifier
	apple  *providers.AppleVerifier
}

func NewService(pool *pgxpool.Pool, secret []byte, ttl time.Duration) *Service {
	return &Service{pool: pool, secret: secret, ttl: ttl}
}

// WithGoogleVerifier wires a Google ID-token verifier into the service.
// Pass nil to leave the provider disabled.
func (s *Service) WithGoogleVerifier(v *providers.GoogleVerifier) *Service {
	s.google = v
	return s
}

// WithAppleVerifier wires an Apple ID-token verifier into the service.
// Pass nil to leave the provider disabled.
func (s *Service) WithAppleVerifier(v *providers.AppleVerifier) *Service {
	s.apple = v
	return s
}

type guestRequest struct {
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl,omitempty"`
}

type tokenResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

func (s *Service) HandleGuest(w http.ResponseWriter, r *http.Request) {
	var req guestRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	name := strings.TrimSpace(req.DisplayName)
	if name == "" {
		httpx.WriteErr(w, httpx.Err(http.StatusBadRequest, "invalid_name", "displayName required"))
		return
	}
	if len(name) > 40 {
		name = name[:40]
	}

	var u User
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO users (display_name, avatar_url) VALUES ($1, $2)
		 RETURNING id, display_name, avatar_url, email, entitlements`,
		name, req.AvatarURL,
	).Scan(&u.ID, &u.DisplayName, &u.AvatarURL, &u.Email, &u.Entitlements)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}

	token, err := s.Issue(u.ID)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, tokenResponse{Token: token, User: u})
}

func (s *Service) HandleMe(w http.ResponseWriter, r *http.Request) {
	u, ok := FromContext(r.Context())
	if !ok {
		httpx.WriteErr(w, httpx.ErrUnauthorized)
		return
	}
	httpx.JSON(w, http.StatusOK, u)
}

func (s *Service) Issue(userID uuid.UUID) (string, error) {
	claims := jwt.MapClaims{
		"sub": userID.String(),
		"exp": time.Now().Add(s.ttl).Unix(),
		"iat": time.Now().Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
}

func (s *Service) Verify(tokenStr string) (uuid.UUID, error) {
	tok, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return s.secret, nil
	})
	if err != nil || !tok.Valid {
		return uuid.Nil, errors.New("invalid token")
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return uuid.Nil, errors.New("invalid claims")
	}
	sub, _ := claims["sub"].(string)
	return uuid.Parse(sub)
}

// LoadUser fetches a user by id. Returns httpx.ErrUnauthorized when not found
// OR when the account has been soft-deleted — both look the same to a
// caller: "this token doesn't unlock anything anymore". That makes the auth
// middleware's job trivial.
func (s *Service) LoadUser(ctx context.Context, id uuid.UUID) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, display_name, avatar_url, email, entitlements
		 FROM users WHERE id = $1 AND deleted_at IS NULL`, id,
	).Scan(&u.ID, &u.DisplayName, &u.AvatarURL, &u.Email, &u.Entitlements)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, httpx.ErrUnauthorized
	}
	return u, err
}

// Middleware authenticates via Authorization: Bearer <jwt>.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			httpx.WriteErr(w, httpx.ErrUnauthorized)
			return
		}
		userID, err := s.Verify(strings.TrimPrefix(auth, "Bearer "))
		if err != nil {
			httpx.WriteErr(w, httpx.ErrUnauthorized)
			return
		}
		u, err := s.LoadUser(r.Context(), userID)
		if err != nil {
			httpx.WriteErr(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
	})
}

func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

func FromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}

// --- OAuth ------------------------------------------------------------------

type oauthRequest struct {
	// IDToken is the platform-issued ID token (the long JWT the mobile SDK
	// hands back after the user picks an account in the system dialog).
	IDToken string `json:"idToken"`
	// DisplayName / Email are optional, used only as fallbacks. Apple's
	// SDK returns these once-only on first authorisation in a body field
	// outside the ID token, so the client surfaces them here. For Google
	// they're always available inside the token and these fields are
	// ignored.
	DisplayName string `json:"displayName,omitempty"`
	Email       string `json:"email,omitempty"`
}

// HandleGoogle authenticates a Google ID token, finds-or-creates the
// matching Convoy user, and returns our session token.
func (s *Service) HandleGoogle(w http.ResponseWriter, r *http.Request) {
	if s.google == nil {
		httpx.WriteErr(w, httpx.Err(http.StatusServiceUnavailable, "google_disabled", "google sign-in is not configured"))
		return
	}
	s.handleOAuth(w, r, s.google.Verify)
}

// HandleApple authenticates an Apple ID token, finds-or-creates the user,
// and returns our session token. The optional name / email in the body
// are persisted on the very first sign-in (Apple only sends those once).
func (s *Service) HandleApple(w http.ResponseWriter, r *http.Request) {
	if s.apple == nil {
		httpx.WriteErr(w, httpx.Err(http.StatusServiceUnavailable, "apple_disabled", "sign in with apple is not configured"))
		return
	}
	s.handleOAuth(w, r, s.apple.Verify)
}

func (s *Service) handleOAuth(w http.ResponseWriter, r *http.Request, verify func(string) (providers.Identity, error)) {
	var req oauthRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	id, err := verify(req.IDToken)
	if err != nil {
		httpx.WriteErr(w, httpx.Err(http.StatusUnauthorized, "invalid_id_token", "could not verify id token"))
		return
	}

	// Layer the client-supplied fallbacks in: provider data wins when
	// present, the body fields only fill in gaps. Apple's first-time-only
	// email/name is exactly this scenario.
	if id.Email == "" && req.Email != "" {
		id.Email = strings.TrimSpace(req.Email)
	}
	if id.DisplayName == "" && req.DisplayName != "" {
		id.DisplayName = strings.TrimSpace(req.DisplayName)
	}
	if id.DisplayName == "" {
		// Last-resort display name. Anonymous-ish but at least non-empty
		// so room cards don't render with a blank label.
		if id.Email != "" {
			if at := strings.Index(id.Email, "@"); at > 0 {
				id.DisplayName = id.Email[:at]
			}
		}
		if id.DisplayName == "" {
			id.DisplayName = "convoy_user"
		}
	}
	if len(id.DisplayName) > 40 {
		id.DisplayName = id.DisplayName[:40]
	}

	u, err := s.upsertOAuthUser(r.Context(), id)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	token, err := s.Issue(u.ID)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, tokenResponse{Token: token, User: u})
}

// upsertOAuthUser is the find-or-create heart of OAuth sign-in. Wrapped in a
// transaction so a concurrent first-sign-in for the same (provider,subject)
// pair doesn't double-insert. Returns the canonical User for the caller to
// hand back to the client.
func (s *Service) upsertOAuthUser(ctx context.Context, id providers.Identity) (User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rolled back implicitly on commit

	var userID uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT user_id FROM user_identities WHERE provider = $1 AND subject = $2`,
		id.Provider, id.Subject,
	).Scan(&userID)

	switch {
	case err == nil:
		// Existing user — refresh email if the provider sent a newer one.
		// (Display name is intentionally NOT updated post-creation: users
		// may rename themselves in-app and we don't want a re-sign-in to
		// stomp on that.)
		if id.Email != "" {
			_, _ = tx.Exec(ctx,
				`UPDATE users SET email = $1 WHERE id = $2 AND (email IS DISTINCT FROM $1)`,
				id.Email, userID,
			)
		}
	case errors.Is(err, pgx.ErrNoRows):
		// First sign-in. Create the user row + identity row.
		var avatar *string
		if id.AvatarURL != "" {
			a := id.AvatarURL
			avatar = &a
		}
		var emailPtr *string
		if id.Email != "" {
			e := id.Email
			emailPtr = &e
		}
		if err := tx.QueryRow(ctx,
			`INSERT INTO users (display_name, avatar_url, email)
			 VALUES ($1, $2, $3) RETURNING id`,
			id.DisplayName, avatar, emailPtr,
		).Scan(&userID); err != nil {
			return User{}, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_identities (user_id, provider, subject, email)
			 VALUES ($1, $2, $3, $4)`,
			userID, id.Provider, id.Subject, emailPtr,
		); err != nil {
			return User{}, err
		}
	default:
		return User{}, err
	}

	var u User
	if err := tx.QueryRow(ctx,
		`SELECT id, display_name, avatar_url, email, entitlements
		 FROM users WHERE id = $1 AND deleted_at IS NULL`, userID,
	).Scan(&u.ID, &u.DisplayName, &u.AvatarURL, &u.Email, &u.Entitlements); err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return u, nil
}

// --- Account deletion -------------------------------------------------------

// HandleDeleteAccount is the in-app account-deletion endpoint required by the
// Play Store policy (and Apple's equivalent). Atomic transaction:
//  1. Stamp `deleted_at` so the auth middleware refuses every later request.
//  2. End every room they own (other members get the standard room_ended UX
//     on their next reconnect).
//  3. Mark every active membership as kicked so other rooms don't show them
//     as still "here".
//
// Their session token will keep validating cryptographically until expiry,
// but LoadUser refuses deleted accounts so the token is effectively dead.
func (s *Service) HandleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	u, ok := FromContext(r.Context())
	if !ok {
		httpx.WriteErr(w, httpx.ErrUnauthorized)
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck

	if _, err := tx.Exec(r.Context(),
		`UPDATE users SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, u.ID,
	); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE rooms SET ended_at = now() WHERE owner_id = $1 AND ended_at IS NULL`, u.ID,
	); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE room_members SET kicked = true, left_at = now()
		 WHERE user_id = $1 AND left_at IS NULL`, u.ID,
	); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
