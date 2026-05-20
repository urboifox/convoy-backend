package realtime

import (
	"encoding/json"

	"github.com/google/uuid"

	"github.com/convoy/backend/internal/rooms"
)

const (
	MsgSnapshot        = "snapshot"
	MsgMemberJoined    = "member_joined"
	MsgMemberLeft      = "member_left"
	MsgLocation        = "loc"
	MsgMuted           = "muted"
	MsgKicked          = "kicked"
	MsgRoomEnded       = "room_ended"
	MsgDestination     = "destination"
	MsgError           = "error"
	MsgPong            = "pong"
)

const (
	ClientMsgLocation = "loc"
	ClientMsgPing     = "ping"
)

type Envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type Location struct {
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	Heading float64 `json:"heading"`
	Speed   float64 `json:"speed"`
}

type LocationEvent struct {
	UserID  uuid.UUID `json:"userId"`
	Lat     float64   `json:"lat"`
	Lng     float64   `json:"lng"`
	Heading float64   `json:"heading"`
	Speed   float64   `json:"speed"`
	TS      int64     `json:"ts"`
}

type SnapshotPayload struct {
	Self        uuid.UUID                   `json:"self"`
	Members     []rooms.Member              `json:"members"`
	Locations   map[uuid.UUID]LocationEvent `json:"locations"`
	Destination *rooms.Destination          `json:"destination,omitempty"`
}

type DestinationPayload struct {
	Destination *rooms.Destination `json:"destination"`
}

type MemberJoinedPayload struct {
	Member rooms.Member `json:"member"`
}

type MemberLeftPayload struct {
	UserID uuid.UUID `json:"userId"`
}

type MutedPayload struct {
	UserID uuid.UUID `json:"userId"`
	Muted  bool      `json:"muted"`
	By     uuid.UUID `json:"by"`
}

type KickedPayload struct {
	UserID uuid.UUID `json:"userId"`
	By     uuid.UUID `json:"by"`
}

type ErrorPayload struct {
	Message string `json:"message"`
}

func pack(t string, payload any) ([]byte, error) {
	if payload == nil {
		return json.Marshal(Envelope{Type: t})
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{Type: t, Payload: raw})
}
