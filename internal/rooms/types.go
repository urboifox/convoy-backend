package rooms

import (
	"time"

	"github.com/google/uuid"
)

const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

type Room struct {
	ID        uuid.UUID  `json:"id"`
	Code      string     `json:"code"`
	Name      *string    `json:"name,omitempty"`
	OwnerID   uuid.UUID  `json:"ownerId"`
	CreatedAt time.Time  `json:"createdAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

type Member struct {
	UserID      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
	AvatarURL   *string   `json:"avatarUrl,omitempty"`
	Role        string    `json:"role"`
	Muted       bool      `json:"muted"`
	JoinedAt    time.Time `json:"joinedAt"`
}

type Destination struct {
	Lat   float64   `json:"lat"`
	Lng   float64   `json:"lng"`
	SetAt time.Time `json:"setAt"`
	SetBy uuid.UUID `json:"setBy"`
}

type RoomDetail struct {
	Room
	Members     []Member     `json:"members"`
	Destination *Destination `json:"destination,omitempty"`
	// PresentUserIDs is the set of members currently connected to the room
	// websocket. Clients use this to render absent members differently from
	// active ones (faded card in the drawer, lower count next to the icon).
	PresentUserIDs []uuid.UUID `json:"presentUserIds"`
	// EmergencyUserIDs is the set of members currently flagged as needing
	// emergency help. Live-only state held by the realtime hub; included
	// here so a freshly-reloaded client hydrates the red routes / banner
	// before the websocket reconnects.
	EmergencyUserIDs []uuid.UUID `json:"emergencyUserIds"`
}

type ActiveRoom struct {
	ID   uuid.UUID `json:"id"`
	Code string    `json:"code"`
	Name *string   `json:"name,omitempty"`
	Role string    `json:"role"`
	// MemberCount is the number of members currently connected (present) to
	// the room — not the total membership. A user that has the room saved
	// but is not on the room screen is excluded.
	MemberCount int       `json:"memberCount"`
	JoinedAt    time.Time `json:"joinedAt"`
}
