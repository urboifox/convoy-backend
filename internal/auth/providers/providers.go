// Package providers verifies OAuth ID tokens issued by Google and Apple
// and turns them into the minimal user-identity bits Convoy stores.
//
// Both providers issue OIDC-compatible ID tokens (RS256 for Google, ES256
// for Apple) and publish their signing keys at a JWKS URL. The verifier
// fetches & caches that JWKS, validates the signature, then enforces
// audience / issuer / expiry. Anything that doesn't pass returns a typed
// `ErrInvalidToken` so the HTTP layer can return 401 cleanly.
//
// We deliberately avoid storing the raw provider tokens anywhere — only
// the surfaced identity (subject, email, name) flows into our DB.
package providers

import "errors"

// Identity is the post-verification shape consumed by the auth service.
// Every field except Subject is optional because providers vary in what
// they include (Apple, in particular, only returns name/email on the
// very first authorisation).
type Identity struct {
	Provider    string // "google" | "apple"
	Subject     string // provider-stable user id (the `sub` claim)
	Email       string // empty when not asked / not granted
	DisplayName string // empty when not provided
	AvatarURL   string // populated by Google's `picture` claim only
}

// ErrInvalidToken is returned for any token that fails verification —
// signature, issuer, audience, expiry, malformed shape. The caller maps
// this to HTTP 401 without leaking which check failed (avoids giving
// would-be attackers a debugging oracle).
var ErrInvalidToken = errors.New("invalid id token")
