# Tweedle

Monitoring agent for Linux servers (Go). Watches selected processes, system
resources and Podman containers and reports problems as events (info, warn,
error). The app "Dee" shows the state and receives push messages.

Plan and background: wiki, Tools → "Tweedle & Dee (Monitoring)".

```bash
go build -o tweedle ./cmd/tweedle   # build
go test ./...                       # tests

sudo ./tweedle setup     # choose what to watch → /etc/tweedle/config.yaml
sudo ./tweedle status    # current state once
sudo ./tweedle serve     # run the checks and the API (systemd service)
sudo ./tweedle pair      # QR code + code to pair the Dee app (single use, 10 min)
sudo ./tweedle devices   # list paired devices; "devices rm ID" removes one
sudo ./tweedle testpush  # send a test push to all devices
```

## Push

Events go through the Expo Push Service (outgoing HTTPS only). Each device
gets the levels it chose (`minLevel`: info, warn, error, none), in its own
language, on the Android channel `info`, `warn` or `error` (the app creates
these channels). Events within `push.debounce` (3s) are collected; more than
three for one device become one summary message. Tokens Expo reports as
`DeviceNotRegistered` are removed. `push.access_token` is only needed if
enhanced push security is on in the Expo account.

## API

nginx forwards `/mon/` to `listen` (default `127.0.0.1:9300`) with the prefix
stripped. Everything except `/health` and `/api/pair` needs
`Authorization: Bearer <token>` (token comes from pairing).

| Route | |
|---|---|
| `GET /health` | `{ok, name, version}` |
| `POST /api/pair` | `{code, name, lang, pushToken?}` → `{token, deviceId, name}` |
| `GET /api/status` | current snapshot + unseen warn/error count |
| `GET /api/history?range=1h\|6h\|24h\|7d` | minute values (max. 360 points) |
| `GET /api/events?before=ID&limit=50&minLevel=warn` | events, newest first, `message` in the device language |
| `POST /api/events/seen` | `{upTo: ID}` |
| `GET/PUT/DELETE /api/device` | own settings (`name, lang, pushToken, minLevel`), unpair |
| `GET /api/live` | WebSocket: `{type:"status"}` every tick, `{type:"event"}` per event |

`--config PATH` uses another config file. For testing as a normal user,
`podman_cmd: [sudo, -n, podman]` in the config reads the root containers.
