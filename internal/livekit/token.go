package livekit

import (
	"fmt"
	"strings"
	"time"

	lkauth "github.com/livekit/protocol/auth"
	"github.com/google/uuid"
)

type Config struct {
	URL       string
	APIKey    string
	APISecret string
}

func (c Config) Enabled() bool {
	return c.URL != "" && c.APIKey != "" && c.APISecret != ""
}

// NormalizeURL ensures the client receives a WebSocket URL.
func NormalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "wss://") || strings.HasPrefix(raw, "ws://") {
		return raw
	}
	if strings.HasPrefix(raw, "https://") {
		return "wss://" + strings.TrimPrefix(raw, "https://")
	}
	if strings.HasPrefix(raw, "http://") {
		return "ws://" + strings.TrimPrefix(raw, "http://")
	}
	return "wss://" + raw
}

func IssueRoomToken(cfg Config, roomID, userID uuid.UUID, displayName string) (token string, err error) {
	if !cfg.Enabled() {
		return "", fmt.Errorf("livekit is not configured")
	}

	grant := &lkauth.VideoGrant{
		RoomJoin: true,
		Room:     roomID.String(),
	}

	at := lkauth.NewAccessToken(cfg.APIKey, cfg.APISecret).
		SetIdentity(userID.String()).
		SetValidFor(6 * time.Hour).
		SetVideoGrant(grant)

	if displayName != "" {
		at.SetName(displayName)
	}

	return at.ToJWT()
}
