package feedback

import (
	"net/http"
	"strings"

	"github.com/convoy/backend/internal/auth"
	"github.com/convoy/backend/internal/httpx"
)

const maxMessageLen = 2000

type Handlers struct{ store *Store }

func NewHandlers(store *Store) *Handlers { return &Handlers{store: store} }

type submitReq struct {
	Message string `json:"message"`
}

// Submit accepts user feedback. Authenticated via auth.Middleware; the user is
// pulled from the request context.
func (h *Handlers) Submit(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		httpx.WriteErr(w, httpx.ErrUnauthorized)
		return
	}

	var req submitReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		httpx.WriteErr(w, httpx.Err(http.StatusBadRequest, "empty_message", "message is required"))
		return
	}
	if len(msg) > maxMessageLen {
		msg = msg[:maxMessageLen]
	}

	entry, err := h.store.Create(r.Context(), user.ID, msg)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, entry)
}
