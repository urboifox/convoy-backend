package rooms

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("room not found")

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) CreateRoom(ctx context.Context, ownerID uuid.UUID, code string, name *string) (*Room, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var r Room
	if err := tx.QueryRow(ctx,
		`INSERT INTO rooms (code, name, owner_id) VALUES ($1, $2, $3)
		 RETURNING id, code, name, owner_id, created_at, ended_at`,
		code, name, ownerID,
	).Scan(&r.ID, &r.Code, &r.Name, &r.OwnerID, &r.CreatedAt, &r.EndedAt); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO room_members (room_id, user_id, role) VALUES ($1, $2, $3)`,
		r.ID, ownerID, RoleOwner,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (*Room, error) {
	var r Room
	err := s.pool.QueryRow(ctx,
		`SELECT id, code, name, owner_id, created_at, ended_at
		 FROM rooms WHERE id = $1`, id,
	).Scan(&r.ID, &r.Code, &r.Name, &r.OwnerID, &r.CreatedAt, &r.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &r, err
}

func (s *Store) GetActiveByCode(ctx context.Context, code string) (*Room, error) {
	var r Room
	err := s.pool.QueryRow(ctx,
		`SELECT id, code, name, owner_id, created_at, ended_at
		 FROM rooms WHERE code = $1 AND ended_at IS NULL`, code,
	).Scan(&r.ID, &r.Code, &r.Name, &r.OwnerID, &r.CreatedAt, &r.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &r, err
}

// UpsertMember adds a user to a room or re-activates a previous (left/kicked) membership.
func (s *Store) UpsertMember(ctx context.Context, roomID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO room_members (room_id, user_id, role)
		 VALUES ($1, $2, 'member')
		 ON CONFLICT (room_id, user_id) DO UPDATE
		 SET left_at = NULL, kicked = FALSE`,
		roomID, userID,
	)
	return err
}

func (s *Store) MarkLeft(ctx context.Context, roomID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE room_members SET left_at = now()
		 WHERE room_id = $1 AND user_id = $2 AND left_at IS NULL`,
		roomID, userID,
	)
	return err
}

func (s *Store) SetMuted(ctx context.Context, roomID, userID uuid.UUID, muted bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE room_members SET muted = $3
		 WHERE room_id = $1 AND user_id = $2 AND left_at IS NULL`,
		roomID, userID, muted,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Kick(ctx context.Context, roomID, userID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE room_members SET kicked = TRUE, left_at = now()
		 WHERE room_id = $1 AND user_id = $2 AND left_at IS NULL`,
		roomID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) EndRoom(ctx context.Context, roomID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE rooms SET ended_at = now() WHERE id = $1 AND ended_at IS NULL`,
		roomID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetMember(ctx context.Context, roomID, userID uuid.UUID) (Member, error) {
	var m Member
	err := s.pool.QueryRow(ctx,
		`SELECT rm.user_id, u.display_name, u.avatar_url, rm.role, rm.muted, rm.joined_at
		 FROM room_members rm
		 JOIN users u ON u.id = rm.user_id
		 WHERE rm.room_id = $1 AND rm.user_id = $2 AND rm.left_at IS NULL`,
		roomID, userID,
	).Scan(&m.UserID, &m.DisplayName, &m.AvatarURL, &m.Role, &m.Muted, &m.JoinedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Member{}, ErrNotFound
	}
	return m, err
}

func (s *Store) ListMembers(ctx context.Context, roomID uuid.UUID) ([]Member, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT rm.user_id, u.display_name, u.avatar_url, rm.role, rm.muted, rm.joined_at
		 FROM room_members rm
		 JOIN users u ON u.id = rm.user_id
		 WHERE rm.room_id = $1 AND rm.left_at IS NULL
		 ORDER BY rm.joined_at ASC`, roomID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Member, 0)
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.DisplayName, &m.AvatarURL, &m.Role, &m.Muted, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetDestination(ctx context.Context, roomID uuid.UUID) (*Destination, error) {
	var d Destination
	err := s.pool.QueryRow(ctx,
		`SELECT dest_lat, dest_lng, dest_name, dest_notes, dest_set_at, dest_set_by
		 FROM rooms WHERE id = $1 AND dest_lat IS NOT NULL AND dest_lng IS NOT NULL`,
		roomID,
	).Scan(&d.Lat, &d.Lng, &d.Name, &d.Notes, &d.SetAt, &d.SetBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) SetDestination(ctx context.Context, roomID, setBy uuid.UUID, lat, lng float64, name, notes *string) (*Destination, error) {
	var d Destination
	err := s.pool.QueryRow(ctx,
		`UPDATE rooms SET dest_lat = $2, dest_lng = $3, dest_name = $4, dest_notes = $5, dest_set_at = now(), dest_set_by = $6
		 WHERE id = $1 AND ended_at IS NULL
		 RETURNING dest_lat, dest_lng, dest_name, dest_notes, dest_set_at, dest_set_by`,
		roomID, lat, lng, name, notes, setBy,
	).Scan(&d.Lat, &d.Lng, &d.Name, &d.Notes, &d.SetAt, &d.SetBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &d, err
}

// UpdateDestinationMeta rewrites just the destination's name/notes, leaving the
// pin (and set_at/set_by) untouched. Returns ErrNotFound if no destination is
// currently set, so an edit can't resurrect a cleared destination.
func (s *Store) UpdateDestinationMeta(ctx context.Context, roomID uuid.UUID, name, notes *string) (*Destination, error) {
	var d Destination
	err := s.pool.QueryRow(ctx,
		`UPDATE rooms SET dest_name = $2, dest_notes = $3
		 WHERE id = $1 AND ended_at IS NULL AND dest_lat IS NOT NULL AND dest_lng IS NOT NULL
		 RETURNING dest_lat, dest_lng, dest_name, dest_notes, dest_set_at, dest_set_by`,
		roomID, name, notes,
	).Scan(&d.Lat, &d.Lng, &d.Name, &d.Notes, &d.SetAt, &d.SetBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &d, err
}

func (s *Store) UpdateName(ctx context.Context, roomID uuid.UUID, name *string) (*Room, error) {
	var r Room
	err := s.pool.QueryRow(ctx,
		`UPDATE rooms SET name = $2
		 WHERE id = $1 AND ended_at IS NULL
		 RETURNING id, code, name, owner_id, created_at, ended_at`,
		roomID, name,
	).Scan(&r.ID, &r.Code, &r.Name, &r.OwnerID, &r.CreatedAt, &r.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &r, err
}

func (s *Store) ClearDestination(ctx context.Context, roomID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE rooms SET dest_lat = NULL, dest_lng = NULL, dest_set_at = NULL, dest_set_by = NULL
		 WHERE id = $1 AND ended_at IS NULL`,
		roomID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetPersonalMarker returns the viewer's own private pin for a room, or nil if
// they haven't placed one. Scoped to (room, user) — never returns another
// member's marker.
func (s *Store) GetPersonalMarker(ctx context.Context, roomID, userID uuid.UUID) (*PersonalMarker, error) {
	var pm PersonalMarker
	err := s.pool.QueryRow(ctx,
		`SELECT lat, lng, label, updated_at
		 FROM personal_markers WHERE room_id = $1 AND user_id = $2`,
		roomID, userID,
	).Scan(&pm.Lat, &pm.Lng, &pm.Label, &pm.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pm, nil
}

// UpsertPersonalMarker creates or moves the viewer's private pin in place.
func (s *Store) UpsertPersonalMarker(ctx context.Context, roomID, userID uuid.UUID, lat, lng float64, label *string) (*PersonalMarker, error) {
	var pm PersonalMarker
	err := s.pool.QueryRow(ctx,
		`INSERT INTO personal_markers (room_id, user_id, lat, lng, label, updated_at)
		 VALUES ($1, $2, $3, $4, $5, now())
		 ON CONFLICT (room_id, user_id) DO UPDATE
		 SET lat = EXCLUDED.lat, lng = EXCLUDED.lng, label = EXCLUDED.label, updated_at = now()
		 RETURNING lat, lng, label, updated_at`,
		roomID, userID, lat, lng, label,
	).Scan(&pm.Lat, &pm.Lng, &pm.Label, &pm.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &pm, nil
}

// DeletePersonalMarker removes the viewer's private pin. A no-op (no error) if
// they didn't have one.
func (s *Store) DeletePersonalMarker(ctx context.Context, roomID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM personal_markers WHERE room_id = $1 AND user_id = $2`,
		roomID, userID,
	)
	return err
}

// ListStops returns a room's shared stops in route order (ascending position,
// then insertion time to break ties).
func (s *Store) ListStops(ctx context.Context, roomID uuid.UUID) ([]Stop, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, room_id, lat, lng, label, position, created_by, created_at
		 FROM room_stops WHERE room_id = $1
		 ORDER BY position ASC, created_at ASC`, roomID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Stop, 0)
	for rows.Next() {
		var st Stop
		if err := rows.Scan(&st.ID, &st.RoomID, &st.Lat, &st.Lng, &st.Label, &st.Position, &st.CreatedBy, &st.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// AddStop appends a stop at the end of the room's ordered list. The new
// position is one past the current max (0 for the first stop).
func (s *Store) AddStop(ctx context.Context, roomID, createdBy uuid.UUID, lat, lng float64, label *string) (*Stop, error) {
	var st Stop
	err := s.pool.QueryRow(ctx,
		`INSERT INTO room_stops (room_id, lat, lng, label, position, created_by)
		 VALUES ($1, $2, $3, $4,
		     COALESCE((SELECT MAX(position) + 1 FROM room_stops WHERE room_id = $1), 0),
		     $5)
		 RETURNING id, room_id, lat, lng, label, position, created_by, created_at`,
		roomID, lat, lng, label, createdBy,
	).Scan(&st.ID, &st.RoomID, &st.Lat, &st.Lng, &st.Label, &st.Position, &st.CreatedBy, &st.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// ClearStops removes every stop in a room. Used when the destination — and so
// the whole planned route — is cleared.
func (s *Store) ClearStops(ctx context.Context, roomID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM room_stops WHERE room_id = $1`, roomID)
	return err
}

// RemoveStop deletes a stop. Scoped to roomID so a stop id from another room
// can't be removed. Returns ErrNotFound when nothing matched.
func (s *Store) RemoveStop(ctx context.Context, roomID, stopID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM room_stops WHERE id = $1 AND room_id = $2`, stopID, roomID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateStopLabel sets (or, with a nil label, clears) a stop's custom title.
// Scoped to roomID so a stop id from another room can't be edited. Returns
// ErrNotFound when nothing matched.
func (s *Store) UpdateStopLabel(ctx context.Context, roomID, stopID uuid.UUID, label *string) (*Stop, error) {
	var st Stop
	err := s.pool.QueryRow(ctx,
		`UPDATE room_stops SET label = $3
		 WHERE id = $1 AND room_id = $2
		 RETURNING id, room_id, lat, lng, label, position, created_by, created_at`,
		stopID, roomID, label,
	).Scan(&st.ID, &st.RoomID, &st.Lat, &st.Lng, &st.Label, &st.Position, &st.CreatedBy, &st.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// TransferOwnership hands the room to another active member: it points
// rooms.owner_id at the new owner and swaps the two members' roles, all in one
// transaction. The new owner must be an active, non-kicked member (ErrNotFound
// otherwise).
func (s *Store) TransferOwnership(ctx context.Context, roomID, oldOwnerID, newOwnerID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(
		     SELECT 1 FROM room_members
		     WHERE room_id = $1 AND user_id = $2 AND left_at IS NULL AND kicked = FALSE
		 )`,
		roomID, newOwnerID,
	).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}

	tag, err := tx.Exec(ctx,
		`UPDATE rooms SET owner_id = $2 WHERE id = $1 AND ended_at IS NULL`,
		roomID, newOwnerID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	if _, err := tx.Exec(ctx,
		`UPDATE room_members SET role = $3 WHERE room_id = $1 AND user_id = $2`,
		roomID, newOwnerID, RoleOwner,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE room_members SET role = $3 WHERE room_id = $1 AND user_id = $2`,
		roomID, oldOwnerID, RoleMember,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ActiveMembership returns the user's row in the room iff they are an active, non-kicked member.
func (s *Store) ActiveMembership(ctx context.Context, roomID, userID uuid.UUID) (role string, muted bool, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT role, muted FROM room_members
		 WHERE room_id = $1 AND user_id = $2 AND left_at IS NULL AND kicked = FALSE`,
		roomID, userID,
	).Scan(&role, &muted)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrNotFound
	}
	return role, muted, err
}

// ListActiveRoomsForUser returns every room the user is still a member of.
// `MemberCount` is left at zero here — the service layer fills it in with the
// live presence count from the realtime hub, so we don't waste a per-row
// subquery on a value that's about to be overwritten.
func (s *Store) ListActiveRoomsForUser(ctx context.Context, userID uuid.UUID) ([]ActiveRoom, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT r.id, r.code, r.name, rm.role, rm.joined_at
		 FROM room_members rm
		 JOIN rooms r ON r.id = rm.room_id
		 WHERE rm.user_id = $1
		   AND rm.left_at IS NULL
		   AND rm.kicked = FALSE
		   AND r.ended_at IS NULL
		 ORDER BY rm.joined_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ActiveRoom, 0)
	for rows.Next() {
		var ar ActiveRoom
		if err := rows.Scan(&ar.ID, &ar.Code, &ar.Name, &ar.Role, &ar.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, ar)
	}
	return out, rows.Err()
}

// CreateMessage inserts a chat line and returns it joined with the author's
// current display name / avatar in a single round-trip.
func (s *Store) CreateMessage(ctx context.Context, roomID, userID uuid.UUID, body string) (*Message, error) {
	var m Message
	err := s.pool.QueryRow(ctx,
		`WITH ins AS (
		     INSERT INTO messages (room_id, user_id, body)
		     VALUES ($1, $2, $3)
		     RETURNING id, room_id, user_id, body, created_at
		 )
		 SELECT ins.id, ins.room_id, ins.user_id, u.display_name, u.avatar_url, ins.body, ins.created_at
		 FROM ins JOIN users u ON u.id = ins.user_id`,
		roomID, userID, body,
	).Scan(&m.ID, &m.RoomID, &m.UserID, &m.DisplayName, &m.AvatarURL, &m.Body, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// ListMessages returns up to `limit` messages newest-first. When `beforeID` is
// set, only messages strictly older than that message are returned — the
// client passes the id of the oldest line it already holds to page backwards.
// Ordering and the cursor both use (created_at, id) so ties on created_at are
// broken deterministically and never skip or repeat a row across pages.
func (s *Store) ListMessages(ctx context.Context, roomID uuid.UUID, beforeID *uuid.UUID, limit int) ([]Message, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT m.id, m.room_id, m.user_id, u.display_name, u.avatar_url, m.body, m.created_at
		 FROM messages m
		 JOIN users u ON u.id = m.user_id
		 WHERE m.room_id = $1
		   AND (
		       $2::uuid IS NULL
		       OR (m.created_at, m.id) < (SELECT created_at, id FROM messages WHERE id = $2)
		   )
		 ORDER BY m.created_at DESC, m.id DESC
		 LIMIT $3`,
		roomID, beforeID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Message, 0, limit)
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.RoomID, &m.UserID, &m.DisplayName, &m.AvatarURL, &m.Body, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
