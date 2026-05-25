package providers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// GoogleJWKSURL is Google's public JWKS endpoint. Stable URL, served from
// google.com; refreshed by Google roughly daily.
const GoogleJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"

// googleIssuers are the two accepted `iss` values for Google ID tokens. Both
// are documented as valid and the library may emit either depending on the
// account type / endpoint used.
var googleIssuers = []string{
	"https://accounts.google.com",
	"accounts.google.com",
}

// GoogleVerifier turns a Google ID token into our Identity shape. Construct
// once at startup (it spins up a JWKS background refresher) and reuse for
// every request.
type GoogleVerifier struct {
	keyfunc keyfunc.Keyfunc
	// audiences are the OAuth client IDs we accept. Typically one each for
	// iOS / Android / Web — Google issues a different `aud` per platform so
	// we have to allow all of them. Empty = reject everything.
	audiences []string
}

// NewGoogleVerifier constructs the verifier and starts the JWKS background
// refresh. `audiences` should list every OAuth 2.0 client id the mobile
// clients may present (iOS / Android / Web). Returns an error if the JWKS
// can't be fetched on startup; production should fail-fast on this rather
// than silently accepting nothing.
func NewGoogleVerifier(ctx context.Context, audiences []string) (*GoogleVerifier, error) {
	if len(audiences) == 0 {
		return nil, errors.New("google verifier: at least one audience (client id) required")
	}
	jwks, err := keyfunc.NewDefaultCtx(ctx, []string{GoogleJWKSURL})
	if err != nil {
		return nil, fmt.Errorf("fetch google jwks: %w", err)
	}
	return &GoogleVerifier{keyfunc: jwks, audiences: audiences}, nil
}

// Verify parses and validates `idToken`. On success returns the extracted
// Identity; on any failure returns ErrInvalidToken (wrapped) so callers can
// uniformly map to 401.
func (v *GoogleVerifier) Verify(idToken string) (Identity, error) {
	idToken = strings.TrimSpace(idToken)
	if idToken == "" {
		return Identity{}, ErrInvalidToken
	}
	claims := jwt.MapClaims{}
	tok, err := jwt.ParseWithClaims(idToken, &claims, v.keyfunc.Keyfunc,
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !tok.Valid {
		return Identity{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	iss, _ := claims["iss"].(string)
	if !contains(googleIssuers, iss) {
		return Identity{}, fmt.Errorf("%w: unexpected issuer %q", ErrInvalidToken, iss)
	}
	aud, _ := claims["aud"].(string)
	if !contains(v.audiences, aud) {
		return Identity{}, fmt.Errorf("%w: audience %q not allowed", ErrInvalidToken, aud)
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return Identity{}, fmt.Errorf("%w: missing subject", ErrInvalidToken)
	}

	// Belt-and-suspenders: `WithExpirationRequired` already covers this,
	// but keep an explicit check so the error path is local & obvious.
	if exp, err := claims.GetExpirationTime(); err == nil && exp != nil && time.Now().After(exp.Time) {
		return Identity{}, fmt.Errorf("%w: expired", ErrInvalidToken)
	}

	id := Identity{Provider: "google", Subject: sub}
	if e, ok := claims["email"].(string); ok {
		// Google's `email_verified` is a boolean (or string in some
		// edge cases). Treat anything other than literal true as
		// "unverified" — defensive against minor schema drift.
		if v, ok := claims["email_verified"].(bool); ok && v {
			id.Email = e
		} else if v, ok := claims["email_verified"].(string); ok && v == "true" {
			id.Email = e
		}
	}
	if n, ok := claims["name"].(string); ok {
		id.DisplayName = n
	}
	if p, ok := claims["picture"].(string); ok {
		id.AvatarURL = p
	}
	return id, nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
