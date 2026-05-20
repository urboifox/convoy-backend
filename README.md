# convoy-backend

Realtime backend for the Convoy mobile app. Go + Postgres + WebSocket.

## Quick start

```bash
cp .env.example .env
# edit JWT_SECRET

make db-up          # start postgres on :5432
make dev            # run the API on :8080
```

Schema migrations run automatically at boot.

## Endpoints

REST (all return JSON):

| Method | Path                                  | Auth | Purpose                                 |
| ------ | ------------------------------------- | ---- | --------------------------------------- |
| GET    | `/healthz`                            | -    | liveness                                |
| POST   | `/auth/guest`                         | -    | create a user, returns `{ token, user }`|
| GET    | `/me`                                 | jwt  | current user                            |
| GET    | `/rooms/active`                       | jwt  | convoys you are still in (not left / not ended) |
| POST   | `/rooms`                              | jwt  | create a room (caller becomes owner)    |
| POST   | `/rooms/join`                         | jwt  | join by `{ code }`                      |
| GET    | `/rooms/{id}`                         | jwt  | room detail + members + destination     |
| POST   | `/rooms/{id}/leave`                   | jwt  | leave the room                          |
| PUT    | `/rooms/{id}/destination`             | jwt  | owner — set single convoy destination `{ lat, lng }` |
| DELETE | `/rooms/{id}/destination`             | jwt  | owner — clear destination               |
| POST   | `/rooms/{id}/members/{uid}/mute`      | jwt  | owner only — `{ muted: bool }`          |
| POST   | `/rooms/{id}/members/{uid}/kick`      | jwt  | owner only                              |
| DELETE | `/rooms/{id}`                         | jwt  | owner only — end room                   |
| POST   | `/rooms/{id}/voice/token`             | jwt  | LiveKit join token `{ token, url }` (requires `LIVEKIT_*` env) |

WebSocket:

```
GET /ws?room=<uuid>&token=<jwt>
```

Frames are JSON: `{ "type": "...", "payload": {...} }`.

Client → server: `loc`, `ping`.
Server → client: `snapshot`, `member_joined`, `member_left`, `loc`, `muted`, `kicked`, `room_ended`, `destination`, `error`, `pong`.

## Architecture

- `internal/auth`        — JWT issuance + bearer middleware
- `internal/rooms`       — store / service / handlers (single responsibility per file)
- `internal/realtime`    — in-memory hub, per-connection client, WS upgrade handler
- `internal/db`          — pgxpool connect + embedded SQL migrations
- `internal/httpx`       — typed error + JSON helpers

REST mutations call into the `realtime.Hub` via the `rooms.Broadcaster` interface, so the HTTP layer never imports `realtime` and there's no cycle.
