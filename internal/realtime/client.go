package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/convoy/backend/internal/auth"
)

const (
	sendBuffer   = 32
	writeTimeout = 5 * time.Second
)

type client struct {
	conn   *websocket.Conn
	user   auth.User
	roomID uuid.UUID
	send   chan []byte

	closeOnce sync.Once
	closed    chan struct{}
}

func newClient(conn *websocket.Conn, user auth.User, roomID uuid.UUID) *client {
	return &client{
		conn:   conn,
		user:   user,
		roomID: roomID,
		send:   make(chan []byte, sendBuffer),
		closed: make(chan struct{}),
	}
}

func (c *client) enqueue(b []byte) {
	select {
	case c.send <- b:
	default:
		// slow consumer — drop this message and disconnect them.
		// real-time data is fine to drop; correctness is delivered by snapshots on reconnect.
		c.close("send buffer full")
	}
}

func (c *client) close(reason string) {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.conn.Close(websocket.StatusNormalClosure, reason)
	})
}

func (c *client) writePump(ctx context.Context, pingInterval time.Duration) {
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		case msg := <-c.send:
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				c.close("write error")
				return
			}
		case <-ping.C:
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.conn.Ping(wctx)
			cancel()
			if err != nil {
				c.close("ping failed")
				return
			}
		}
	}
}

func (c *client) readPump(ctx context.Context, hub *Hub) {
	defer c.close("read loop ended")

	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				slog.Debug("ws read closed", "user", c.user.ID, "err", err)
			}
			return
		}

		var env Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			sendTo(c, MsgError, ErrorPayload{Message: "invalid frame"})
			continue
		}

		switch env.Type {
		case ClientMsgPing:
			sendTo(c, MsgPong, nil)

		case ClientMsgLocation:
			var loc Location
			if err := json.Unmarshal(env.Payload, &loc); err != nil {
				sendTo(c, MsgError, ErrorPayload{Message: "invalid location"})
				continue
			}
			ev := LocationEvent{
				UserID:  c.user.ID,
				Lat:     loc.Lat,
				Lng:     loc.Lng,
				Heading: loc.Heading,
				Speed:   loc.Speed,
				TS:      time.Now().UnixMilli(),
			}
			hub.recordLocation(c.roomID, ev)
			self := c.user.ID
			hub.broadcast(c.roomID, MsgLocation, ev, &self)

		default:
			sendTo(c, MsgError, ErrorPayload{Message: "unknown message type"})
		}
	}
}
