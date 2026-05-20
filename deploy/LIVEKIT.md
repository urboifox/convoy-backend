# LiveKit deployment for Convoy voice

If the app shows **"Voice could not connect"** or **"couldn't establish pc connection"**, the problem is almost always that the phone cannot establish a WebRTC peer connection to the LiveKit server. The Convoy app talks to LiveKit directly — the Go API only mints the access token.

This guide walks through the production setup for `convoy.arabic4u.org`.

---

## 1. Backend env vars

Set these in the Go API's environment (`.env` next to the binary, or whatever your service manager uses):

```env
LIVEKIT_URL=wss://livekit.arabic4u.org
LIVEKIT_API_KEY=APIxxxxxxxx
LIVEKIT_API_SECRET=secretxxxxxxxxxxxxxxxxxxxxxxxxxx
```

`LIVEKIT_URL` is what the phone receives in the `POST /rooms/{id}/voice/token` response, so it must be the **public** wss:// URL.

---

## 2. LiveKit server config (`/etc/livekit/config.yaml`)

```yaml
port: 7880
bind_addresses:
  - 0.0.0.0

rtc:
  # public IP of the box LiveKit runs on
  use_external_ip: true     # auto-detect (works on most VPS)
  # external_ip: 203.0.113.4  # OR pin it explicitly

  # UDP port range used for media (open these in the firewall)
  port_range_start: 50000
  port_range_end:   60000

  # TCP fallback for ICE (also open this)
  tcp_port: 7881

keys:
  APIxxxxxxxx: secretxxxxxxxxxxxxxxxxxxxxxxxxxx

logging:
  level: info

# TURN is optional but strongly recommended for mobile clients on
# carrier networks / corporate Wi-Fi where UDP is blocked.
turn:
  enabled: true
  domain: livekit.arabic4u.org   # must resolve to this box
  tls_port: 5349                 # or 443 if you don't already use it
  udp_port: 3478
  # paths to a real TLS cert (LetsEncrypt is fine; can be the same cert as nginx)
  cert_file: /etc/letsencrypt/live/livekit.arabic4u.org/fullchain.pem
  key_file:  /etc/letsencrypt/live/livekit.arabic4u.org/privkey.pem
```

---

## 3. Firewall

```bash
# WebSocket signaling (behind nginx)
ufw allow 443/tcp

# WebRTC UDP media (the big one)
ufw allow 50000:60000/udp

# WebRTC TCP fallback
ufw allow 7881/tcp

# TURN
ufw allow 3478/udp
ufw allow 5349/tcp
```

If LiveKit runs on a cloud VPS (DigitalOcean, AWS, etc.), open the same ports in the cloud security group too.

---

## 4. nginx (signaling only)

LiveKit's signaling traffic is a normal WebSocket — proxy it like the Convoy API:

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 443 ssl http2;
    server_name livekit.arabic4u.org;

    ssl_certificate     /etc/letsencrypt/live/livekit.arabic4u.org/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/livekit.arabic4u.org/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:7880;
        proxy_http_version 1.1;
        proxy_set_header Upgrade            $http_upgrade;
        proxy_set_header Connection         $connection_upgrade;
        proxy_set_header Host               $host;
        proxy_set_header X-Real-IP          $remote_addr;
        proxy_set_header X-Forwarded-For    $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto  $scheme;
        proxy_read_timeout 86400s;
        proxy_send_timeout 86400s;
    }
}
```

**Do not proxy the UDP media ports through nginx.** They must reach the LiveKit process directly via the firewall rules above.

---

## 5. Verify

From the server:

```bash
sudo ss -tulpn | grep -E 'livekit|7880|7881|3478|5349'
```

You should see LiveKit listening on `7880/tcp` (signaling) and have UDP `3478` / TCP `7881` / TCP `5349` reachable from the public internet.

Then use LiveKit's built-in connection tester from a browser on the **mobile data** network of the device that's failing: <https://livekit.io/connection-test>. Point it at `wss://livekit.arabic4u.org` and watch which ICE candidates succeed.

---

## 6. Force TURN-only from the app (debugging)

If voice works on Wi-Fi but fails on mobile, drop a TURN server in the app's `.env` and rebuild:

```env
EXPO_PUBLIC_LIVEKIT_TURN_URL=turns:livekit.arabic4u.org:5349?transport=tcp
EXPO_PUBLIC_LIVEKIT_TURN_USERNAME=convoy
EXPO_PUBLIC_LIVEKIT_TURN_PASSWORD=...
EXPO_PUBLIC_LIVEKIT_ICE_RELAY=true     # forces relay-only — for testing
```

Once voice works with `EXPO_PUBLIC_LIVEKIT_ICE_RELAY=true`, you know the issue is your UDP firewall (not the LiveKit credentials or token).
