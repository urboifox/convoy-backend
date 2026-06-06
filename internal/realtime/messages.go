package realtime

import (
	"encoding/json"

	"github.com/google/uuid"

	"github.com/convoy/backend/internal/rooms"
)

const (
	MsgSnapshot      = "snapshot"
	MsgMemberJoined  = "member_joined"
	MsgMemberLeft    = "member_left"
	MsgMemberPresent = "member_present"
	MsgMemberAbsent  = "member_absent"
	MsgLocation      = "loc"
	MsgMuted         = "muted"
	MsgKicked        = "kicked"
	MsgRoomEnded     = "room_ended"
	MsgRoomRenamed   = "room_renamed"
	MsgDestination   = "destination"
	MsgEmergency     = "emergency"
	MsgError         = "error"
	MsgPong          = "pong"
)

const (
	ClientMsgLocation  = "loc"
	ClientMsgEmergency = "emergency"
	ClientMsgPing      = "ping"
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
	// PresentUserIDs lists the members currently connected to the room socket.
	// Members that exist in `Members` but are absent from this list have the
	// room in their list of active convoys but are not on the room screen
	// right now (backgrounded app, on home page, etc).
	PresentUserIDs []uuid.UUID `json:"presentUserIds"`
	// EmergencyUserIDs lists members who have raised an active emergency.
	// Live-only state: cleared automatically when a member leaves / goes
	// absent, so a reconnecting client always re-derives it from the
	// snapshot rather than relying on retained sets.
	EmergencyUserIDs []uuid.UUID `json:"emergencyUserIds"`
}

type DestinationPayload struct {
	Destination *rooms.Destination `json:"destination"`
}

type RoomRenamedPayload struct {
	Name *string `json:"name"`
}

type MemberJoinedPayload struct {
	Member rooms.Member `json:"member"`
}

type MemberLeftPayload struct {
	UserID uuid.UUID `json:"userId"`
}

type MemberPresentPayload struct {
	UserID uuid.UUID `json:"userId"`
}

type MemberAbsentPayload struct {
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

// EmergencyPayload announces a user's emergency state. `Active=true` means
// they are now requesting help; `Active=false` cleared the request (either
// the user themselves stood it down, or the server cleared it because they
// went absent). The flag is included on both transitions so clients can
// treat the message as a state-set, not a state-toggle.
type EmergencyPayload struct {
	UserID uuid.UUID `json:"userId"`
	Active bool      `json:"active"`
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
