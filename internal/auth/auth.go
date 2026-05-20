package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/convoy/backend/internal/httpx"
)

type ctxKey struct{}

type User struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"displayName"`
	AvatarURL   *string   `json:"avatarUrl,omitempty"`
}

type Service struct {
	pool   *pgxpool.Pool
	secret []byte
	ttl    time.Duration
}

func NewService(pool *pgxpool.Pool, secret []byte, ttl time.Duration) *Service {
	return &Service{pool: pool, secret: secret, ttl: ttl}
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
		 RETURNING id, display_name, avatar_url`,
		name, req.AvatarURL,
	).Scan(&u.ID, &u.DisplayName, &u.AvatarURL)
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
// so it composes cleanly with the auth middleware.
func (s *Service) LoadUser(ctx context.Context, id uuid.UUID) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, display_name, avatar_url FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.DisplayName, &u.AvatarURL)
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
