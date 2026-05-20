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
}

type ActiveRoom struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        *string   `json:"name,omitempty"`
	Role        string    `json:"role"`
	MemberCount int       `json:"memberCount"`
	JoinedAt    time.Time `json:"joinedAt"`
}
