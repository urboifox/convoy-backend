package feedback

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Create inserts a feedback row. userID is required (the API is authenticated)
// but the column itself is nullable so admin deletes of users don't wipe their
// historical feedback.
func (s *Store) Create(ctx context.Context, userID uuid.UUID, message string) (*Entry, error) {
	var e Entry
	var displayName *string
	err := s.pool.QueryRow(ctx,
		`WITH inserted AS (
			INSERT INTO feedback (user_id, message) VALUES ($1, $2)
			RETURNING id, user_id, message, created_at
		)
		SELECT i.id, i.user_id, i.message, i.created_at, u.display_name
		FROM inserted i
		LEFT JOIN users u ON u.id = i.user_id`,
		userID, message,
	).Scan(&e.ID, &e.UserID, &e.Message, &e.CreatedAt, &displayName)
	if err != nil {
		return nil, err
	}
	if displayName != nil {
		e.AuthorName = *displayName
	}
	return &e, nil
}

// List returns entries newest-first, up to `limit`. `before` (optional) returns
// rows strictly older than the given timestamp for cursor pagination.
func (s *Store) List(ctx context.Context, limit int, before *string) ([]Entry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	var rows pgx.Rows
	var err error

	if before != nil && *before != "" {
		rows, err = s.pool.Query(ctx,
			`SELECT f.id, f.user_id, COALESCE(u.display_name, '') AS author_name, f.message, f.created_at
			 FROM feedback f
			 LEFT JOIN users u ON u.id = f.user_id
			 WHERE f.created_at < $1
			 ORDER BY f.created_at DESC
			 LIMIT $2`,
			*before, limit,
		)
	} else {
		rows, err = s.pool.Query(ctx,
			`SELECT f.id, f.user_id, COALESCE(u.display_name, '') AS author_name, f.message, f.created_at
			 FROM feedback f
			 LEFT JOIN users u ON u.id = f.user_id
			 ORDER BY f.created_at DESC
			 LIMIT $1`,
			limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Entry, 0, limit)
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.UserID, &e.AuthorName, &e.Message, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Count returns the total number of feedback rows. Used by the dashboard
// summary widget; cheap on Postgres for small tables.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM feedback`).Scan(&n)
	return n, err
}
