package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// userRow is the per-row shape rendered by the users list / detail pages.
// Kept flat so templates don't have to traverse nested structures.
type userRow struct {
	ID           uuid.UUID
	DisplayName  string
	Email        string // empty for guests / pre-OAuth accounts
	AvatarURL    string
	CreatedAt    time.Time
	DeletedAt    *time.Time
	Providers    []string // collected from user_identities
	ActiveRooms  int      // rooms they currently own that haven't ended
	JoinedRooms  int      // active memberships
}

// usersList page (paged via simple LIMIT/OFFSET ?p= query). Lists newest
// users first. Filter `?q=` matches a substring of display_name or email.
func (m *Module) usersList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	q := strings.TrimSpace(r.URL.Query().Get("q"))

	rows, err := m.queryUsers(ctx, q, 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m.render(w, "users.html", map[string]any{
		"Title":     "Users",
		"NavActive": "users",
		"Users":     rows,
		"Query":     q,
	})
}

// userDetail shows one user, their identities, and exposes the hard-delete
// action. Hard-delete cascades through every related row (rooms they owned,
// memberships, identities, feedback, push tokens) because of the FK
// `ON DELETE CASCADE` we set in the original schema.
func (m *Module) userDetail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	row, providers, err := m.fetchUser(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m.render(w, "user_detail.html", map[string]any{
		"Title":     "User · " + row.DisplayName,
		"NavActive": "users",
		"User":      row,
		"Providers": providers,
	})
}

// userHardDelete physically removes the user row. Used by an admin to purge
// already-soft-deleted accounts (or, in incident response, to remove an
// abusive account immediately). Cascades through every related row.
func (m *Module) userHardDelete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if _, err := m.cfg.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if isHTMX(r) {
		// HTMX swaps the page back to the list.
		w.Header().Set("HX-Redirect", "/admin/users")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

// userSoftDelete is the admin-side equivalent of the user-facing DELETE
// /account. Used to take a problematic account offline without immediately
// destroying records (still recoverable for a window).
func (m *Module) userSoftDelete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if _, err := m.cfg.Pool.Exec(ctx,
		`UPDATE users SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := m.cfg.Pool.Exec(ctx,
		`UPDATE rooms SET ended_at = now() WHERE owner_id = $1 AND ended_at IS NULL`, id,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := m.cfg.Pool.Exec(ctx,
		`UPDATE room_members SET kicked = true, left_at = now() WHERE user_id = $1 AND left_at IS NULL`, id,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/users/"+id.String(), http.StatusFound)
}

// userRestore clears the soft-delete flag. Useful when a user emails support
// asking to undo a regretted deletion within the 30-day window.
func (m *Module) userRestore(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if _, err := m.cfg.Pool.Exec(ctx,
		`UPDATE users SET deleted_at = NULL WHERE id = $1`, id,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/users/"+id.String(), http.StatusFound)
}

// --- queries ---------------------------------------------------------------

// queryUsers loads up to `limit` users matching an optional substring query.
// LEFT JOIN aggregates pull provider list and room counts so the table can
// render without N+1.
func (m *Module) queryUsers(ctx context.Context, q string, limit int) ([]userRow, error) {
	sql := `
		SELECT u.id, u.display_name, COALESCE(u.email, ''), COALESCE(u.avatar_url, ''),
		       u.created_at, u.deleted_at,
		       COALESCE((SELECT array_agg(DISTINCT provider) FROM user_identities WHERE user_id = u.id), '{}') AS providers,
		       (SELECT count(*) FROM rooms WHERE owner_id = u.id AND ended_at IS NULL) AS active_owned,
		       (SELECT count(*) FROM room_members WHERE user_id = u.id AND left_at IS NULL) AS active_joined
		FROM users u
	`
	args := []any{}
	if q != "" {
		sql += `WHERE u.display_name ILIKE $1 OR u.email ILIKE $1 `
		args = append(args, "%"+q+"%")
	}
	sql += `ORDER BY u.created_at DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)

	rows, err := m.cfg.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []userRow{}
	for rows.Next() {
		var r userRow
		if err := rows.Scan(&r.ID, &r.DisplayName, &r.Email, &r.AvatarURL,
			&r.CreatedAt, &r.DeletedAt, &r.Providers, &r.ActiveRooms, &r.JoinedRooms); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (m *Module) fetchUser(ctx context.Context, id uuid.UUID) (userRow, []identity, error) {
	var r userRow
	err := m.cfg.Pool.QueryRow(ctx, `
		SELECT u.id, u.display_name, COALESCE(u.email, ''), COALESCE(u.avatar_url, ''),
		       u.created_at, u.deleted_at,
		       COALESCE((SELECT array_agg(DISTINCT provider) FROM user_identities WHERE user_id = u.id), '{}') AS providers,
		       (SELECT count(*) FROM rooms WHERE owner_id = u.id AND ended_at IS NULL),
		       (SELECT count(*) FROM room_members WHERE user_id = u.id AND left_at IS NULL)
		FROM users u WHERE id = $1
	`, id).Scan(&r.ID, &r.DisplayName, &r.Email, &r.AvatarURL,
		&r.CreatedAt, &r.DeletedAt, &r.Providers, &r.ActiveRooms, &r.JoinedRooms)
	if err != nil {
		return userRow{}, nil, err
	}

	rows, err := m.cfg.Pool.Query(ctx,
		`SELECT provider, subject, COALESCE(email, ''), created_at
		 FROM user_identities WHERE user_id = $1 ORDER BY created_at ASC`, id)
	if err != nil {
		return r, nil, err
	}
	defer rows.Close()

	ids := []identity{}
	for rows.Next() {
		var x identity
		if err := rows.Scan(&x.Provider, &x.Subject, &x.Email, &x.CreatedAt); err != nil {
			return r, nil, err
		}
		ids = append(ids, x)
	}
	return r, ids, rows.Err()
}

type identity struct {
	Provider  string
	Subject   string
	Email     string
	CreatedAt time.Time
}
