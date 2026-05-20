package rooms

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/convoy/backend/internal/auth"
	"github.com/convoy/backend/internal/httpx"
	lk "github.com/convoy/backend/internal/livekit"
)

type Handlers struct {
	svc    *Service
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
	r.Post("/{roomID}/leave", h.leave)
	r.Put("/{roomID}/destination", h.putDestination)
	r.Delete("/{roomID}/destination", h.deleteDestination)
	r.Post("/{roomID}/members/{userID}/mute", h.mute)
	r.Post("/{roomID}/members/{userID}/kick", h.kick)
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
	detail, err := h.svc.Detail(r.Context(), room.ID)
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
	detail, err := h.svc.Detail(r.Context(), room.ID)
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
	detail, err := h.svc.Detail(r.Context(), roomID)
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

type destinationReq struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
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
	d, err := h.svc.SetDestination(r.Context(), roomID, actor.ID, req.Lat, req.Lng)
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
