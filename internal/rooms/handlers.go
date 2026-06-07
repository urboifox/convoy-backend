package rooms

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/convoy/backend/internal/auth"
	"github.com/convoy/backend/internal/httpx"
	lk "github.com/convoy/backend/internal/livekit"
)

type Handlers struct {
	svc     *Service
	livekit lk.Config
}

func NewHandlers(svc *Service, livekit lk.Config) *Handlers {
	return &Handlers{svc: svc, livekit: livekit}
}

func (h *Handlers) Routes(r chi.Router) {
	r.Get("/active", h.listActive)
	r.Post("/", h.create)
	r.Post("/join", h.join)
	r.Get("/{roomID}", h.get)
	r.Put("/{roomID}/name", h.rename)
	r.Post("/{roomID}/leave", h.leave)
	r.Put("/{roomID}/destination", h.putDestination)
	r.Patch("/{roomID}/destination", h.patchDestination)
	r.Delete("/{roomID}/destination", h.deleteDestination)
	r.Put("/{roomID}/personal-marker", h.putPersonalMarker)
	r.Delete("/{roomID}/personal-marker", h.deletePersonalMarker)
	r.Post("/{roomID}/stops", h.addStop)
	r.Delete("/{roomID}/stops", h.clearStops)
	r.Patch("/{roomID}/stops/{stopID}", h.patchStop)
	r.Delete("/{roomID}/stops/{stopID}", h.removeStop)
	r.Get("/{roomID}/messages", h.listMessages)
	r.Post("/{roomID}/messages", h.postMessage)
	r.Post("/{roomID}/members/{userID}/mute", h.mute)
	r.Post("/{roomID}/members/{userID}/kick", h.kick)
	r.Post("/{roomID}/members/{userID}/owner", h.transferOwner)
	r.Delete("/{roomID}", h.end)
	r.Post("/{roomID}/voice/token", h.voiceToken)
}

type voiceTokenRes struct {
	Token string `json:"token"`
	URL   string `json:"url"`
}

func (h *Handlers) voiceToken(w http.ResponseWriter, r *http.Request) {
	if !h.livekit.Enabled() {
		httpx.WriteErr(w, httpx.Err(http.StatusServiceUnavailable, "voice_unavailable", "voice is not configured on the server"))
		return
	}

	user, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	if _, err := h.svc.AssertMember(r.Context(), roomID, user.ID); err != nil {
		httpx.WriteErr(w, err)
		return
	}

	token, err := lk.IssueRoomToken(h.livekit, roomID, user.ID, user.DisplayName)
	if err != nil {
		httpx.WriteErr(w, httpx.Err(http.StatusInternalServerError, "voice_token_failed", "could not create voice token"))
		return
	}

	httpx.JSON(w, http.StatusOK, voiceTokenRes{
		Token: token,
		URL:   lk.NormalizeURL(h.livekit.URL),
	})
}

func (h *Handlers) listActive(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.FromContext(r.Context())
	list, err := h.svc.ListActiveForUser(r.Context(), user.ID)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

type createReq struct {
	Name *string `json:"name,omitempty"`
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.FromContext(r.Context())
	var req createReq
	_ = httpx.Decode(r, &req) // body is optional

	room, err := h.svc.Create(r.Context(), user.ID, req.Name)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	detail, err := h.svc.Detail(r.Context(), room.ID, user.ID)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, detail)
}

type joinReq struct {
	Code string `json:"code"`
}

func (h *Handlers) join(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.FromContext(r.Context())
	var req joinReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	room, err := h.svc.Join(r.Context(), user.ID, req.Code)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	detail, err := h.svc.Detail(r.Context(), room.ID, user.ID)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, detail)
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	if _, err := h.svc.AssertMember(r.Context(), roomID, user.ID); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	detail, err := h.svc.Detail(r.Context(), roomID, user.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.WriteErr(w, httpx.ErrNotFound)
			return
		}
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, detail)
}

func (h *Handlers) leave(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	if err := h.svc.Leave(r.Context(), roomID, user.ID); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type renameReq struct {
	Name string `json:"name"`
}

func (h *Handlers) rename(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	var req renameReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	room, err := h.svc.Rename(r.Context(), roomID, actor.ID, req.Name)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, room)
}

type muteReq struct {
	Muted bool `json:"muted"`
}

func (h *Handlers) mute(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	var req muteReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	if err := h.svc.Mute(r.Context(), roomID, actor.ID, targetID, req.Muted); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) kick(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	if err := h.svc.Kick(r.Context(), roomID, actor.ID, targetID); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) transferOwner(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	if err := h.svc.TransferOwnership(r.Context(), roomID, actor.ID, targetID); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type destinationReq struct {
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
	Name  *string `json:"name"`
	Notes *string `json:"notes"`
}

// destinationMetaReq is the body for editing a set destination's name/notes
// without moving it. Both fields are sent in full by the client; omitting one
// (or sending blank) clears it.
type destinationMetaReq struct {
	Name  *string `json:"name"`
	Notes *string `json:"notes"`
}

func (h *Handlers) putDestination(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	var req destinationReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	d, err := h.svc.SetDestination(r.Context(), roomID, actor.ID, req.Lat, req.Lng, req.Name, req.Notes)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, d)
}

func (h *Handlers) patchDestination(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	var req destinationMetaReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	d, err := h.svc.UpdateDestinationMeta(r.Context(), roomID, actor.ID, req.Name, req.Notes)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, d)
}

func (h *Handlers) deleteDestination(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	if err := h.svc.ClearDestination(r.Context(), roomID, actor.ID); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type personalMarkerReq struct {
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
	Label *string `json:"label,omitempty"`
}

func (h *Handlers) putPersonalMarker(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	var req personalMarkerReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	pm, err := h.svc.SetPersonalMarker(r.Context(), roomID, user.ID, req.Lat, req.Lng, req.Label)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, pm)
}

func (h *Handlers) deletePersonalMarker(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	if err := h.svc.ClearPersonalMarker(r.Context(), roomID, user.ID); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type addStopReq struct {
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
	Label *string `json:"label,omitempty"`
}

func (h *Handlers) addStop(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	var req addStopReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	stop, err := h.svc.AddStop(r.Context(), roomID, actor.ID, req.Lat, req.Lng, req.Label)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, stop)
}

type updateStopReq struct {
	Label *string `json:"label,omitempty"`
}

func (h *Handlers) patchStop(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	stopID, err := uuid.Parse(chi.URLParam(r, "stopID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	var req updateStopReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	stop, err := h.svc.UpdateStopLabel(r.Context(), roomID, actor.ID, stopID, req.Label)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, stop)
}

func (h *Handlers) removeStop(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	stopID, err := uuid.Parse(chi.URLParam(r, "stopID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	if err := h.svc.RemoveStop(r.Context(), roomID, actor.ID, stopID); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) clearStops(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	if err := h.svc.ClearStops(r.Context(), roomID, actor.ID); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type sendMessageReq struct {
	Body string `json:"body"`
}

func (h *Handlers) postMessage(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	var req sendMessageReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	msg, err := h.svc.PostMessage(r.Context(), roomID, user.ID, req.Body)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, msg)
}

func (h *Handlers) listMessages(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}

	// Cursor pagination: `before` is the id of the oldest message the client
	// already holds; omit it for the most recent page.
	var beforeID *uuid.UUID
	if raw := r.URL.Query().Get("before"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteErr(w, httpx.ErrBadRequest)
			return
		}
		beforeID = &id
	}

	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}

	msgs, err := h.svc.ListMessages(r.Context(), roomID, user.ID, beforeID, limit)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, msgs)
}

func (h *Handlers) end(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.FromContext(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		httpx.WriteErr(w, httpx.ErrBadRequest)
		return
	}
	if err := h.svc.End(r.Context(), roomID, actor.ID); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
