package push

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Upsert (re)registers a token for the calling user. We key on the token
// (not user_id) because the same handset may switch between guest sessions;
// each unique token still maps to exactly one row.
func (s *Store) Upsert(ctx context.Context, userID uuid.UUID, token, platform string) error {
	if platform != "ios" && platform != "android" {
		return errors.New("invalid platform")
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO push_tokens (token, user_id, platform, updated_at)
		 VALUES ($1, $2, $3, now())
		 ON CONFLICT (token) DO UPDATE
		 SET user_id = EXCLUDED.user_id,
		     platform = EXCLUDED.platform,
		     updated_at = now()`,
		token, userID, platform,
	)
	return err
}

// AllTokens returns every registered token. The dashboard never has enough
// users to justify pagination here yet.
func (s *Store) AllTokens(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT token FROM push_tokens`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, 64)
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Count is the live recipient count rendered in the broadcast composer.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM push_tokens`).Scan(&n)
	return n, err
}

// DeleteTokens removes tokens Expo has reported as invalid
// (DeviceNotRegistered / InvalidCredentials). Best-effort; we tolerate
// concurrent inserts.
func (s *Store) DeleteTokens(ctx context.Context, tokens []string) error {
	if len(tokens) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM push_tokens WHERE token = ANY($1)`, tokens)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}
