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
	// emergencies is the set of user IDs that currently have an active
	// emergency. Empty-struct map gives us O(1) add/remove/contains and a
	// trivially small footprint per room.
	emergencies map[uuid.UUID]struct{}
}

func newRoomState() *roomState {
	return &roomState{
		clients:     make(map[uuid.UUID]*client),
		locations:   make(map[uuid.UUID]LocationEvent),
		emergencies: make(map[uuid.UUID]struct{}),
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

// join registers a new socket for `c.user` in `roomID`. Returns true when the
// user transitioned from absent to present (i.e. this is not a reconnect of an
// already-connected session); callers use it to decide whether to broadcast
// `member_present` to the rest of the room.
func (h *Hub) join(roomID uuid.UUID, c *client) (transitioned bool) {
	rs := h.roomFor(roomID, true)
	rs.mu.Lock()
	existing, hadExisting := rs.clients[c.user.ID]
	if hadExisting {
		// same user reconnecting — kick the previous socket. The user was
		// already considered present; don't fire a duplicate transition.
		existing.close("replaced by new connection")
	}
	rs.clients[c.user.ID] = c
	rs.mu.Unlock()
	return !hadExisting
}

// leave removes `c` from `roomID` if it is still the current socket for that
// user. Returns whether the user transitioned to absent — true only when we
// actually removed an active client (not when a replaced socket finishes
// closing after being kicked by a fresher one). When the user transitions
// to absent, `clearedEmergency` reports whether they also had an active
// emergency that had to be cleared — the caller broadcasts that separately
// so other members don't keep chasing a vanishing target.
func (h *Hub) leave(roomID uuid.UUID, c *client) (transitioned bool, clearedEmergency bool) {
	rs := h.roomFor(roomID, false)
	if rs == nil {
		return false, false
	}
	rs.mu.Lock()
	if cur, ok := rs.clients[c.user.ID]; ok && cur == c {
		delete(rs.clients, c.user.ID)
		delete(rs.locations, c.user.ID)
		if _, hadEmergency := rs.emergencies[c.user.ID]; hadEmergency {
			delete(rs.emergencies, c.user.ID)
			clearedEmergency = true
		}
		transitioned = true
	}
	empty := len(rs.clients) == 0
	rs.mu.Unlock()

	if empty {
		h.mu.Lock()
		// re-check under the outer lock: someone may have joined while we
		// were unlocked — never drop a room that still has connected clients
		if rs2 := h.rooms[roomID]; rs2 != nil {
			rs2.mu.RLock()
			stillEmpty := len(rs2.clients) == 0
			rs2.mu.RUnlock()
			if stillEmpty {
				delete(h.rooms, roomID)
			}
		}
		h.mu.Unlock()
	}
	return transitioned, clearedEmergency
}

// SetEmergency flips a member's emergency flag. Returns whether the flag
// actually changed — callers only broadcast on transitions so reconnecting
// clients that resend the current state don't spam everyone.
func (h *Hub) SetEmergency(roomID, userID uuid.UUID, active bool) (changed bool) {
	rs := h.roomFor(roomID, true)
	rs.mu.Lock()
	defer rs.mu.Unlock()
	_, has := rs.emergencies[userID]
	if active && !has {
		rs.emergencies[userID] = struct{}{}
		return true
	}
	if !active && has {
		delete(rs.emergencies, userID)
		return true
	}
	return false
}

// EmergencyUserIDs returns every user currently flagged emergency in
// `roomID`. The slice is freshly allocated and safe to mutate.
func (h *Hub) EmergencyUserIDs(roomID uuid.UUID) []uuid.UUID {
	rs := h.roomFor(roomID, false)
	if rs == nil {
		return []uuid.UUID{}
	}
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	out := make([]uuid.UUID, 0, len(rs.emergencies))
	for uid := range rs.emergencies {
		out = append(out, uid)
	}
	return out
}

// PresentUserIDs returns the user IDs of every client currently connected to
// `roomID`. The slice is freshly allocated and safe to mutate.
func (h *Hub) PresentUserIDs(roomID uuid.UUID) []uuid.UUID {
	rs := h.roomFor(roomID, false)
	if rs == nil {
		return []uuid.UUID{}
	}
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	out := make([]uuid.UUID, 0, len(rs.clients))
	for uid := range rs.clients {
		out = append(out, uid)
	}
	return out
}

// PresentCount returns the number of clients currently connected to `roomID`.
func (h *Hub) PresentCount(roomID uuid.UUID) int {
	rs := h.roomFor(roomID, false)
	if rs == nil {
		return 0
	}
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return len(rs.clients)
}

// IsPresent reports whether `userID` has an open socket on `roomID`.
func (h *Hub) IsPresent(roomID, userID uuid.UUID) bool {
	rs := h.roomFor(roomID, false)
	if rs == nil {
		return false
	}
	rs.mu.RLock()
	_, ok := rs.clients[userID]
	rs.mu.RUnlock()
	return ok
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
	present := make([]uuid.UUID, 0, len(rs.clients))
	for uid := range rs.clients {
		present = append(present, uid)
	}
	emergencies := make([]uuid.UUID, 0, len(rs.emergencies))
	for uid := range rs.emergencies {
		emergencies = append(emergencies, uid)
	}
	rs.mu.RUnlock()
	return SnapshotPayload{
		Self:             selfID,
		Members:          members,
		Locations:        locs,
		Destination:      dest,
		PresentUserIDs:   present,
		EmergencyUserIDs: emergencies,
	}, nil
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

func (h *Hub) BroadcastMemberJoined(roomID uuid.UUID, member rooms.Member) {
	h.broadcast(roomID, MsgMemberJoined, MemberJoinedPayload{Member: member}, nil)
}

func (h *Hub) BroadcastMemberLeft(roomID uuid.UUID, userID uuid.UUID) {
	h.broadcast(roomID, MsgMemberLeft, MemberLeftPayload{UserID: userID}, nil)
}

// BroadcastMemberPresent notifies the room that `userID` has opened a socket.
// The newly-connected client is excluded — they receive the same information
// inside their initial snapshot.
func (h *Hub) BroadcastMemberPresent(roomID, userID uuid.UUID) {
	exclude := userID
	h.broadcast(roomID, MsgMemberPresent, MemberPresentPayload{UserID: userID}, &exclude)
}

// BroadcastMemberAbsent notifies the room that `userID`'s socket has closed.
// The leaving client is excluded — its socket is closing and would not
// receive the frame anyway.
func (h *Hub) BroadcastMemberAbsent(roomID, userID uuid.UUID) {
	exclude := userID
	h.broadcast(roomID, MsgMemberAbsent, MemberAbsentPayload{UserID: userID}, &exclude)
}

// BroadcastEmergency announces a user's emergency state transition. The
// originating user is intentionally NOT excluded — they have a UI showing
// their own emergency state and need the server's ack to lock it in, so
// they receive the frame too.
func (h *Hub) BroadcastEmergency(roomID, userID uuid.UUID, active bool) {
	h.broadcast(roomID, MsgEmergency, EmergencyPayload{UserID: userID, Active: active}, nil)
}

func (h *Hub) DisconnectUser(roomID, userID uuid.UUID) {
	rs := h.roomFor(roomID, false)
	if rs == nil {
		return
	}
	rs.mu.RLock()
	c := rs.clients[userID]
	rs.mu.RUnlock()
	if c != nil {
		c.close("left room")
	}
}

func (h *Hub) BroadcastMute(roomID, userID uuid.UUID, muted bool, by uuid.UUID) {
	h.broadcast(roomID, MsgMuted, MutedPayload{UserID: userID, Muted: muted, By: by}, nil)
}

func (h *Hub) KickConnection(roomID, userID, by uuid.UUID) {
	// notify other members first so the kicked user's frame can't race ahead of
	// the socket close on a slow link
	exclude := userID
	h.broadcast(roomID, MsgKicked, KickedPayload{UserID: userID, By: by}, &exclude)

	rs := h.roomFor(roomID, false)
	if rs == nil {
		return
	}
	rs.mu.RLock()
	c := rs.clients[userID]
	rs.mu.RUnlock()
	if c == nil {
		return
	}

	// enqueue the kicked frame directly to the target and let writePump drain it
	// before the socket is closed (writePump.drainSend handles the race)
	sendTo(c, MsgKicked, KickedPayload{UserID: userID, By: by})
	c.close("kicked")
}

func (h *Hub) BroadcastDestination(roomID uuid.UUID, dest *rooms.Destination) {
	h.broadcast(roomID, MsgDestination, DestinationPayload{Destination: dest}, nil)
}

func (h *Hub) BroadcastRoomEnded(roomID uuid.UUID) {
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

	// enqueue, then close — writePump drains pending sends on close so each
	// client receives the room_ended frame before the socket disappears
	for _, c := range conns {
		sendTo(c, MsgRoomEnded, nil)
	}
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

