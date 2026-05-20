package realtime

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/convoy/backend/internal/rooms"
)

// Hub holds in-memory state for every open WebSocket and provides
// fanout primitives to broadcast inside a room.
type Hub struct {
	mu    sync.RWMutex
	rooms map[uuid.UUID]*roomState

	store *rooms.Store
}

type roomState struct {
	mu        sync.RWMutex
	clients   map[uuid.UUID]*client // userID -> client
	locations map[uuid.UUID]LocationEvent
}

func newRoomState() *roomState {
	return &roomState{
		clients:   make(map[uuid.UUID]*client),
		locations: make(map[uuid.UUID]LocationEvent),
	}
}

func NewHub(store *rooms.Store) *Hub {
	return &Hub{
		rooms: make(map[uuid.UUID]*roomState),
		store: store,
	}
}

func (h *Hub) roomFor(id uuid.UUID, createIfMissing bool) *roomState {
	h.mu.RLock()
	rs := h.rooms[id]
	h.mu.RUnlock()
	if rs != nil || !createIfMissing {
		return rs
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if rs = h.rooms[id]; rs != nil {
		return rs
	}
	rs = newRoomState()
	h.rooms[id] = rs
	return rs
}

func (h *Hub) join(roomID uuid.UUID, c *client) {
	rs := h.roomFor(roomID, true)
	rs.mu.Lock()
	if existing, ok := rs.clients[c.user.ID]; ok {
		// same user reconnecting — kick the previous socket
		existing.close("replaced by new connection")
	}
	rs.clients[c.user.ID] = c
	rs.mu.Unlock()
}

func (h *Hub) leave(roomID uuid.UUID, c *client) {
	rs := h.roomFor(roomID, false)
	if rs == nil {
		return
	}
	rs.mu.Lock()
	if cur, ok := rs.clients[c.user.ID]; ok && cur == c {
		delete(rs.clients, c.user.ID)
		delete(rs.locations, c.user.ID)
	}
	empty := len(rs.clients) == 0
	rs.mu.Unlock()

	if empty {
		h.mu.Lock()
		delete(h.rooms, roomID)
		h.mu.Unlock()
	}
}

func (h *Hub) recordLocation(roomID uuid.UUID, ev LocationEvent) {
	rs := h.roomFor(roomID, true)
	rs.mu.Lock()
	rs.locations[ev.UserID] = ev
	rs.mu.Unlock()
}

func (h *Hub) snapshot(ctx context.Context, roomID, selfID uuid.UUID) (SnapshotPayload, error) {
	members, err := h.store.ListMembers(ctx, roomID)
	if err != nil {
		return SnapshotPayload{}, err
	}
	dest, err := h.store.GetDestination(ctx, roomID)
	if err != nil {
		return SnapshotPayload{}, err
	}
	rs := h.roomFor(roomID, true)
	rs.mu.RLock()
	locs := make(map[uuid.UUID]LocationEvent, len(rs.locations))
	for k, v := range rs.locations {
		locs[k] = v
	}
	rs.mu.RUnlock()
	return SnapshotPayload{Self: selfID, Members: members, Locations: locs, Destination: dest}, nil
}

// broadcast sends a message to every connected client in the room.
// `exclude` optionally skips one user (typically the sender).
func (h *Hub) broadcast(roomID uuid.UUID, t string, payload any, exclude *uuid.UUID) {
	rs := h.roomFor(roomID, false)
	if rs == nil {
		return
	}
	data, err := pack(t, payload)
	if err != nil {
		return
	}

	rs.mu.RLock()
	targets := make([]*client, 0, len(rs.clients))
	for uid, c := range rs.clients {
		if exclude != nil && uid == *exclude {
			continue
		}
		targets = append(targets, c)
	}
	rs.mu.RUnlock()

	for _, c := range targets {
		c.enqueue(data)
	}
}

// ---- public broadcaster API used by REST handlers (rooms.Broadcaster) ----

func (h *Hub) BroadcastMute(roomID, userID uuid.UUID, muted bool, by uuid.UUID) {
	h.broadcast(roomID, MsgMuted, MutedPayload{UserID: userID, Muted: muted, By: by}, nil)
}

func (h *Hub) KickConnection(roomID, userID, by uuid.UUID) {
	h.broadcast(roomID, MsgKicked, KickedPayload{UserID: userID, By: by}, nil)

	rs := h.roomFor(roomID, false)
	if rs == nil {
		return
	}
	rs.mu.RLock()
	c := rs.clients[userID]
	rs.mu.RUnlock()
	if c != nil {
		c.close("kicked")
	}
}

func (h *Hub) BroadcastDestination(roomID uuid.UUID, dest *rooms.Destination) {
	h.broadcast(roomID, MsgDestination, DestinationPayload{Destination: dest}, nil)
}

func (h *Hub) BroadcastRoomEnded(roomID uuid.UUID) {
	h.broadcast(roomID, MsgRoomEnded, nil, nil)

	rs := h.roomFor(roomID, false)
	if rs == nil {
		return
	}
	rs.mu.RLock()
	conns := make([]*client, 0, len(rs.clients))
	for _, c := range rs.clients {
		conns = append(conns, c)
	}
	rs.mu.RUnlock()
	for _, c := range conns {
		c.close("room ended")
	}
}

// helper for emitting a typed event to a single client
func sendTo(c *client, t string, payload any) {
	data, err := pack(t, payload)
	if err != nil {
		return
	}
	c.enqueue(data)
}

