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

Client → server: `loc`, `emergency`, `ping`.
Server → client: `snapshot`, `member_joined`, `member_left`, `member_present`, `member_absent`, `loc`, `muted`, `kicked`, `room_ended`, `destination`, `emergency`, `error`, `pong`.

Membership vs presence: `member_joined`/`member_left` reflect REST mutations (join/leave/kick) — they change who is *saved* in the convoy. `member_present`/`member_absent` fire whenever a member opens or closes the `/ws` connection — they reflect who is currently on the room screen. `/rooms/active.memberCount` and `RoomDetail.presentUserIds` both report live presence, not total saved membership.

Emergencies are live-only state held by the hub: any member can send `emergency` with `{ active: bool }` to raise / clear their own flag, the server broadcasts the same event to everyone, and the flag is cleared automatically when the member goes absent (a second `emergency` event with `active: false` is fanned out in that case). `RoomDetail.emergencyUserIds` and `SnapshotPayload.emergencyUserIds` give a fresh client the current set without waiting for individual events.

## Architecture

- `internal/auth`        — JWT issuance + bearer middleware
- `internal/rooms`       — store / service / handlers (single responsibility per file)
- `internal/realtime`    — in-memory hub, per-connection client, WS upgrade handler
- `internal/db`          — pgxpool connect + embedded SQL migrations
- `internal/httpx`       — typed error + JSON helpers

REST mutations call into the `realtime.Hub` via the `rooms.Broadcaster` interface, so the HTTP layer never imports `realtime` and there's no cycle.
