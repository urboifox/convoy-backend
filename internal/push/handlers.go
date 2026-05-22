package push

import (
	"net/http"
	"strings"

	"github.com/convoy/backend/internal/auth"
	"github.com/convoy/backend/internal/httpx"
)

type Handlers struct{ store *Store }

func NewHandlers(store *Store) *Handlers { return &Handlers{store: store} }

type registerReq struct {
	Token    string `json:"token"`
	Platform string `json:"platform"`
}

// Save is the registration endpoint the mobile app calls once a push token
// has been issued by Expo. Idempotent.
func (h *Handlers) Save(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		httpx.WriteErr(w, httpx.ErrUnauthorized)
		return
	}
	var req registerReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	token := strings.TrimSpace(req.Token)
	platform := strings.ToLower(strings.TrimSpace(req.Platform))
	if token == "" || (platform != "ios" && platform != "android") {
		httpx.WriteErr(w, httpx.Err(http.StatusBadRequest, "invalid_token", "token and platform required"))
		return
	}
	if !strings.HasPrefix(token, "ExponentPushToken[") && !strings.HasPrefix(token, "ExpoPushToken[") {
		httpx.WriteErr(w, httpx.Err(http.StatusBadRequest, "invalid_token", "expected Expo Push token"))
		return
	}
	if err := h.store.Upsert(r.Context(), user.ID, token, platform); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusNoContent, nil)
}
