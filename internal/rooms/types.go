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

// PersonalMarker is a private, per-user pin within a room. Only its owner ever
// sees it — it is never broadcast — and it persists so it survives leaving and
// rejoining the room or signing in on another device.
type PersonalMarker struct {
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	Label     *string   `json:"label,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Stop is a shared, ordered rest point along a convoy's route. Owner-managed
// and visible to everyone; the client builds a multi-waypoint route running
// origin -> stops (ascending Position) -> destination.
type Stop struct {
	ID        uuid.UUID `json:"id"`
	RoomID    uuid.UUID `json:"roomId"`
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	Label     *string   `json:"label,omitempty"`
	Position  int       `json:"position"`
	CreatedBy uuid.UUID `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

// Message is a single persisted chat line. DisplayName / AvatarURL are
// denormalised from the author's user row at read time so a client can
// render the message without a separate lookup (and historic messages keep
// showing the author even after they leave the room).
type Message struct {
	ID          uuid.UUID `json:"id"`
	RoomID      uuid.UUID `json:"roomId"`
	UserID      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
	AvatarURL   *string   `json:"avatarUrl,omitempty"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"createdAt"`
}

type RoomDetail struct {
	Room
	Members     []Member     `json:"members"`
	Destination *Destination `json:"destination,omitempty"`
	// PersonalMarker is the *requesting* user's own private pin, if any. It is
	// loaded per-viewer in Detail and never contains another member's marker.
	PersonalMarker *PersonalMarker `json:"personalMarker,omitempty"`
	// Stops are the room's shared, ordered rest points (owner-managed). Always
	// present (possibly empty) so clients can replace their copy wholesale.
	Stops []Stop `json:"stops"`
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
