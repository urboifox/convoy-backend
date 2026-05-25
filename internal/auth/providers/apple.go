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

// AppleJWKSURL is Apple's public JWKS endpoint for Sign in with Apple.
const AppleJWKSURL = "https://appleid.apple.com/auth/keys"

// AppleIssuer is the only valid `iss` for Apple-issued ID tokens.
const AppleIssuer = "https://appleid.apple.com"

// AppleVerifier turns an Apple Sign in ID token into our Identity shape.
// Like the Google verifier, construct once and reuse — the keyfunc runs
// a background JWKS refresh.
type AppleVerifier struct {
	keyfunc keyfunc.Keyfunc
	// audiences is typically just one value: the iOS app's bundle id
	// (e.g. "com.convoy.app"). Apple issues tokens with `aud` set to the
	// requesting client. If you ever add a web flow with a separate
	// Services ID, list it here too.
	audiences []string
}

func NewAppleVerifier(ctx context.Context, audiences []string) (*AppleVerifier, error) {
	if len(audiences) == 0 {
		return nil, errors.New("apple verifier: at least one audience required")
	}
	jwks, err := keyfunc.NewDefaultCtx(ctx, []string{AppleJWKSURL})
	if err != nil {
		return nil, fmt.Errorf("fetch apple jwks: %w", err)
	}
	return &AppleVerifier{keyfunc: jwks, audiences: audiences}, nil
}

// Verify parses and validates `idToken`. Apple-only quirk: the `name` /
// `email` claims are only present on the *very first* authorisation; we
// don't fail when they're absent, the caller may opt to capture them from
// a separate body field (the iOS SDK gives them once-only on first sign in).
func (v *AppleVerifier) Verify(idToken string) (Identity, error) {
	idToken = strings.TrimSpace(idToken)
	if idToken == "" {
		return Identity{}, ErrInvalidToken
	}
	claims := jwt.MapClaims{}
	tok, err := jwt.ParseWithClaims(idToken, &claims, v.keyfunc.Keyfunc,
		jwt.WithValidMethods([]string{"ES256"}),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !tok.Valid {
		return Identity{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	iss, _ := claims["iss"].(string)
	if iss != AppleIssuer {
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
	if exp, err := claims.GetExpirationTime(); err == nil && exp != nil && time.Now().After(exp.Time) {
		return Identity{}, fmt.Errorf("%w: expired", ErrInvalidToken)
	}

	id := Identity{Provider: "apple", Subject: sub}
	if e, ok := claims["email"].(string); ok {
		// Apple's `email_verified` can be a bool or a string literal
		// "true" depending on the SDK version. Accept both shapes.
		switch v := claims["email_verified"].(type) {
		case bool:
			if v {
				id.Email = e
			}
		case string:
			if v == "true" {
				id.Email = e
			}
		default:
			// no verified field — trust the email Apple gave us, since
			// they only forward verified addresses anyway.
			id.Email = e
		}
	}
	return id, nil
}
