package realtime

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/convoy/backend/internal/auth"
	"github.com/convoy/backend/internal/rooms"
)


type Handler struct {
	hub          *Hub
	auth         *auth.Service
	store        *rooms.Store
	pingInterval time.Duration
}

func NewHandler(hub *Hub, authSvc *auth.Service, store *rooms.Store, pingInterval time.Duration) *Handler {
	return &Handler{hub: hub, auth: authSvc, store: store, pingInterval: pingInterval}
}

// ServeHTTP handles GET /ws?room=<uuid>&token=<jwt>.
// Auth is via query param because RN's WebSocket can't set headers reliably.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	roomID, err := uuid.Parse(r.URL.Query().Get("room"))
	if err != nil {
		http.Error(w, "invalid room", http.StatusBadRequest)
		return
	}
	token := r.URL.Query().Get("token")
	userID, err := h.auth.Verify(token)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	room, err := h.store.GetByID(r.Context(), roomID)
	if err != nil {
		if errors.Is(err, rooms.ErrNotFound) {
			http.Error(w, "room not found", http.StatusNotFound)
			return
		}
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if room.EndedAt != nil {
		http.Error(w, "room ended", http.StatusGone)
		return
	}

	role, _, err := h.store.ActiveMembership(r.Context(), roomID, userID)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_ = role

	user, err := h.auth.LoadUser(r.Context(), userID)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // mobile clients; origin check happens via token
	})
	if err != nil {
		return
	}

	c := newClient(conn, user, roomID)
	h.hub.join(roomID, c)

	snap, err := h.hub.snapshot(r.Context(), roomID, userID)
	if err == nil {
		sendTo(c, MsgSnapshot, snap)
	}

	ctx, cancel := context.WithCancel(r.Context())
	go c.writePump(ctx, h.pingInterval)
	c.readPump(ctx, h.hub) // blocks until disconnect
	cancel()

	h.hub.leave(roomID, c)
	// member_left is broadcast from REST Leave; only announce disconnect if still an active member.
	if _, _, memErr := h.store.ActiveMembership(r.Context(), roomID, userID); memErr == nil {
		self := userID
		h.hub.broadcast(roomID, MsgMemberLeft, MemberLeftPayload{UserID: userID}, &self)
	}
}
