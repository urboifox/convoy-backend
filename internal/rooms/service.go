package rooms

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"strings"

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
	DisconnectUser(roomID, userID uuid.UUID)
}

type Service struct {
	store *Store
	rt    Broadcaster
}

func NewService(store *Store, rt Broadcaster) *Service {
	return &Service{store: store, rt: rt}
}

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
const maxCodeAttempts = 8

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
	return &RoomDetail{Room: *r, Members: members, Destination: dest}, nil
}

func (s *Service) ListActiveForUser(ctx context.Context, userID uuid.UUID) ([]ActiveRoom, error) {
	return s.store.ListActiveRoomsForUser(ctx, userID)
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
