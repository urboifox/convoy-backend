package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// broadcastStore is intentionally local to the admin package — broadcasts
// are an admin-only concept and don't need their own top-level package.
type broadcastStore struct{ pool *pgxpool.Pool }

type Broadcast struct {
	ID         uuid.UUID
	Title      string
	Body       string
	SentAt     time.Time
	Recipients int
	Delivered  int
	Failed     int
}

func (s *broadcastStore) Insert(ctx context.Context, title, body string, recipients, delivered, failed int) (*Broadcast, error) {
	var b Broadcast
	err := s.pool.QueryRow(ctx,
		`INSERT INTO broadcasts (title, body, recipients, delivered, failed)
		 VALUES ($1,$2,$3,$4,$5)
		 RETURNING id, title, body, sent_at, recipients, delivered, failed`,
		title, body, recipients, delivered, failed,
	).Scan(&b.ID, &b.Title, &b.Body, &b.SentAt, &b.Recipients, &b.Delivered, &b.Failed)
	return &b, err
}

func (s *broadcastStore) List(ctx context.Context, limit int) ([]Broadcast, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, title, body, sent_at, recipients, delivered, failed
		 FROM broadcasts ORDER BY sent_at DESC LIMIT $1`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Broadcast, 0, limit)
	for rows.Next() {
		var b Broadcast
		if err := rows.Scan(&b.ID, &b.Title, &b.Body, &b.SentAt, &b.Recipients, &b.Delivered, &b.Failed); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *broadcastStore) Count(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM broadcasts`).Scan(&n)
	return n, err
}

// --- handlers ---

func (m *Module) broadcastList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	items, err := m.store.List(ctx, 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m.render(w, "broadcasts.html", map[string]any{
		"Title":     "Broadcasts",
		"Items":     items,
		"NavActive": "broadcasts",
	})
}

func (m *Module) broadcastNewForm(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	n, _ := m.cfg.Push.Count(ctx)
	m.render(w, "broadcast_new.html", map[string]any{
		"Title":     "New broadcast",
		"Count":     n,
		"NavActive": "broadcasts",
	})
}

func (m *Module) broadcastSend(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	body := strings.TrimSpace(r.FormValue("body"))
	if title == "" || body == "" {
		m.render(w, "broadcast_result.html", map[string]any{
			"Error": "Title and body are required.",
		})
		return
	}
	if len(title) > 80 {
		title = title[:80]
	}
	if len(body) > 240 {
		body = body[:240]
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	tokens, err := m.cfg.Push.AllTokens(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(tokens) == 0 {
		m.render(w, "broadcast_result.html", map[string]any{
			"Error": "No devices registered yet.",
		})
		return
	}

	res, _ := m.cfg.Expo.Send(ctx, tokens, title, body, map[string]any{
		"type": "broadcast",
	})

	if len(res.InvalidTokens) > 0 {
		_ = m.cfg.Push.DeleteTokens(ctx, res.InvalidTokens)
	}

	rec := len(tokens)
	if _, err := m.store.Insert(ctx, title, body, rec, res.Delivered, res.Failed); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	m.render(w, "broadcast_result.html", map[string]any{
		"Recipients": rec,
		"Delivered":  res.Delivered,
		"Failed":     res.Failed,
		"Pruned":     len(res.InvalidTokens),
	})
}

