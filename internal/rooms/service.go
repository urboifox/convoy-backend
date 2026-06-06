package rooms

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/convoy/backend/internal/httpx"
)

type Broadcaster interface {
	BroadcastMemberJoined(roomID uuid.UUID, member Member)
	BroadcastMemberLeft(roomID uuid.UUID, userID uuid.UUID)
	BroadcastMute(roomID, userID uuid.UUID, muted bool, byUserID uuid.UUID)
	KickConnection(roomID, userID uuid.UUID, byUserID uuid.UUID)
	BroadcastRoomEnded(roomID uuid.UUID)
	BroadcastDestination(roomID uuid.UUID, dest *Destination)
	BroadcastRoomRenamed(roomID uuid.UUID, name *string)
	BroadcastChatMessage(roomID uuid.UUID, msg Message)
	DisconnectUser(roomID, userID uuid.UUID)
	// PresentUserIDs returns the users currently connected to the room's
	// websocket. Used by REST handlers so /rooms/active and /rooms/:id can
	// report live presence alongside membership.
	PresentUserIDs(roomID uuid.UUID) []uuid.UUID
	PresentCount(roomID uuid.UUID) int
	// EmergencyUserIDs returns the users currently flagged as in an active
	// emergency in the room. Hydrated into REST RoomDetail responses so
	// a fresh client reads the red routes / banner from the first frame.
	EmergencyUserIDs(roomID uuid.UUID) []uuid.UUID
}

type Service struct {
	store *Store
	rt    Broadcaster
}

func NewService(store *Store, rt Broadcaster) *Service {
	return &Service{store: store, rt: rt}
}

// Numeric-only codes so they're easy to read out loud and type on a phone
// keypad. 6 digits => 1,000,000 combinations; collisions are only possible
// among *active* rooms (partial unique index), so the retry loop below is
// more than enough headroom in practice.
const codeAlphabet = "0123456789"
const maxCodeAttempts = 8

const (
	minRoomNameLen = 1
	maxRoomNameLen = 40
)

const (
	maxMessageLen = 2000
	// defaultMessagePage / maxMessagePage bound how many chat lines a single
	// history request returns. The client pages backwards with a cursor, so
	// these keep any one fetch cheap even on rooms with very long histories.
	defaultMessagePage = 50
	maxMessagePage     = 100
)

func generateCode() (string, error) {
	max := big.NewInt(int64(len(codeAlphabet)))
	b := make([]byte, 6)
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = codeAlphabet[n.Int64()]
	}
	return string(b), nil
}

func (s *Service) Create(ctx context.Context, ownerID uuid.UUID, name *string) (*Room, error) {
	for i := 0; i < maxCodeAttempts; i++ {
		code, err := generateCode()
		if err != nil {
			return nil, err
		}
		room, err := s.store.CreateRoom(ctx, ownerID, code, name)
		if err == nil {
			return room, nil
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			continue
		}
		return nil, err
	}
	return nil, errors.New("failed to allocate room code")
}

func (s *Service) Join(ctx context.Context, userID uuid.UUID, code string) (*Room, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	room, err := s.store.GetActiveByCode(ctx, code)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, httpx.Err(http.StatusNotFound, "room_not_found", "no active room for that code")
		}
		return nil, err
	}
	if err := s.store.UpsertMember(ctx, room.ID, userID); err != nil {
		return nil, err
	}
	if s.rt != nil {
		if member, err := s.store.GetMember(ctx, room.ID, userID); err == nil {
			s.rt.BroadcastMemberJoined(room.ID, member)
		}
	}
	return room, nil
}

func (s *Service) Detail(ctx context.Context, roomID uuid.UUID) (*RoomDetail, error) {
	r, err := s.store.GetByID(ctx, roomID)
	if err != nil {
		return nil, err
	}
	members, err := s.store.ListMembers(ctx, roomID)
	if err != nil {
		return nil, err
	}
	dest, err := s.store.GetDestination(ctx, roomID)
	if err != nil {
		return nil, err
	}
	detail := &RoomDetail{Room: *r, Members: members, Destination: dest}
	if s.rt != nil {
		detail.PresentUserIDs = s.rt.PresentUserIDs(roomID)
		detail.EmergencyUserIDs = s.rt.EmergencyUserIDs(roomID)
	} else {
		detail.PresentUserIDs = []uuid.UUID{}
		detail.EmergencyUserIDs = []uuid.UUID{}
	}
	return detail, nil
}

// ListActiveForUser returns every active convoy the user belongs to. The
// `MemberCount` field reports *live presence* — how many members are currently
// connected to the room's websocket — not the total saved membership.
func (s *Service) ListActiveForUser(ctx context.Context, userID uuid.UUID) ([]ActiveRoom, error) {
	rooms, err := s.store.ListActiveRoomsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if s.rt != nil {
		for i := range rooms {
			rooms[i].MemberCount = s.rt.PresentCount(rooms[i].ID)
		}
	} else {
		for i := range rooms {
			rooms[i].MemberCount = 0
		}
	}
	return rooms, nil
}

func (s *Service) SetDestination(ctx context.Context, roomID, actorID uuid.UUID, lat, lng float64) (*Destination, error) {
	if err := s.requireOwner(ctx, roomID, actorID); err != nil {
		return nil, err
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return nil, httpx.Err(http.StatusBadRequest, "invalid_coords", "invalid coordinates")
	}
	d, err := s.store.SetDestination(ctx, roomID, actorID, lat, lng)
	if err != nil {
		return nil, err
	}
	if s.rt != nil {
		s.rt.BroadcastDestination(roomID, d)
	}
	return d, nil
}

func (s *Service) ClearDestination(ctx context.Context, roomID, actorID uuid.UUID) error {
	if err := s.requireOwner(ctx, roomID, actorID); err != nil {
		return err
	}
	if err := s.store.ClearDestination(ctx, roomID); err != nil {
		return err
	}
	if s.rt != nil {
		s.rt.BroadcastDestination(roomID, nil)
	}
	return nil
}

// Rename sets a human-friendly name on the room. Owner-only; broadcasts the
// new name to every connected member so their UI updates live.
func (s *Service) Rename(ctx context.Context, roomID, actorID uuid.UUID, name string) (*Room, error) {
	if err := s.requireOwner(ctx, roomID, actorID); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < minRoomNameLen || n > maxRoomNameLen {
		return nil, httpx.Err(http.StatusBadRequest, "invalid_name", "room name must be 1–40 characters")
	}
	room, err := s.store.UpdateName(ctx, roomID, &name)
	if err != nil {
		return nil, err
	}
	if s.rt != nil {
		s.rt.BroadcastRoomRenamed(roomID, room.Name)
	}
	return room, nil
}

func (s *Service) Leave(ctx context.Context, roomID, userID uuid.UUID) error {
	if err := s.store.MarkLeft(ctx, roomID, userID); err != nil {
		return err
	}
	if s.rt != nil {
		s.rt.BroadcastMemberLeft(roomID, userID)
		s.rt.DisconnectUser(roomID, userID)
	}
	return nil
}

func (s *Service) requireOwner(ctx context.Context, roomID, actorID uuid.UUID) error {
	role, _, err := s.store.ActiveMembership(ctx, roomID, actorID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return httpx.ErrForbidden
		}
		return err
	}
	if role != RoleOwner {
		return httpx.ErrForbidden
	}
	return nil
}

func (s *Service) Mute(ctx context.Context, roomID, actorID, targetID uuid.UUID, muted bool) error {
	if err := s.requireOwner(ctx, roomID, actorID); err != nil {
		return err
	}
	if actorID == targetID {
		return httpx.Err(http.StatusBadRequest, "self_action", "cannot moderate yourself")
	}
	if err := s.store.SetMuted(ctx, roomID, targetID, muted); err != nil {
		return err
	}
	if s.rt != nil {
		s.rt.BroadcastMute(roomID, targetID, muted, actorID)
	}
	return nil
}

func (s *Service) Kick(ctx context.Context, roomID, actorID, targetID uuid.UUID) error {
	if err := s.requireOwner(ctx, roomID, actorID); err != nil {
		return err
	}
	if actorID == targetID {
		return httpx.Err(http.StatusBadRequest, "self_action", "cannot kick yourself")
	}
	if err := s.store.Kick(ctx, roomID, targetID); err != nil {
		return err
	}
	if s.rt != nil {
		s.rt.KickConnection(roomID, targetID, actorID)
	}
	return nil
}

func (s *Service) End(ctx context.Context, roomID, actorID uuid.UUID) error {
	if err := s.requireOwner(ctx, roomID, actorID); err != nil {
		return err
	}
	if err := s.store.EndRoom(ctx, roomID); err != nil {
		return err
	}
	if s.rt != nil {
		s.rt.BroadcastRoomEnded(roomID)
	}
	return nil
}

// PostMessage validates and persists a chat line from an active member, then
// broadcasts it to everyone else connected to the room. The author is excluded
// from the broadcast — they already hold the message via this call's response.
func (s *Service) PostMessage(ctx context.Context, roomID, userID uuid.UUID, body string) (*Message, error) {
	if _, err := s.AssertMember(ctx, roomID, userID); err != nil {
		return nil, err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, httpx.Err(http.StatusBadRequest, "empty_message", "message cannot be empty")
	}
	if utf8.RuneCountInString(body) > maxMessageLen {
		return nil, httpx.Err(http.StatusBadRequest, "message_too_long", "message is too long")
	}
	msg, err := s.store.CreateMessage(ctx, roomID, userID, body)
	if err != nil {
		return nil, err
	}
	if s.rt != nil {
		s.rt.BroadcastChatMessage(roomID, *msg)
	}
	return msg, nil
}

// ListMessages returns a page of chat history (newest-first) for an active
// member. `beforeID` is the cursor — pass nil for the latest page, or the id
// of the oldest line already held to fetch the previous page.
func (s *Service) ListMessages(ctx context.Context, roomID, userID uuid.UUID, beforeID *uuid.UUID, limit int) ([]Message, error) {
	if _, err := s.AssertMember(ctx, roomID, userID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxMessagePage {
		limit = defaultMessagePage
	}
	return s.store.ListMessages(ctx, roomID, beforeID, limit)
}

func (s *Service) AssertMember(ctx context.Context, roomID, userID uuid.UUID) (string, error) {
	role, _, err := s.store.ActiveMembership(ctx, roomID, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", httpx.ErrForbidden
		}
		return "", err
	}
	return role, nil
}
