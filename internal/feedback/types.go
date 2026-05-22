package feedback

import (
	"time"

	"github.com/google/uuid"
)

// Entry is a single user-submitted feedback row. AuthorName is denormalized
// from users.display_name at query time so the admin list renders without an
// extra round trip per row.
type Entry struct {
	ID         uuid.UUID `json:"id"`
	UserID     *uuid.UUID `json:"userId,omitempty"`
	AuthorName string    `json:"authorName"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"createdAt"`
}
