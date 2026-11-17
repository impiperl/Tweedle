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

## Endpoints

`endpoints:` in the config lists URLs that must answer; only entries with
`enabled: true` are checked. Each tick every enabled URL
is requested once (new connection each time). Anything below HTTP 400 counts
as up, or exactly `status` if set; a timeout (default 10s), connection or TLS
error raises `endpoint_down` (error), an answer slower than `slow` (if set)
`endpoint_slow` (warn). A new config contains the Prod watcher switched off;
on Dev it is switched on:

```yaml
endpoints:
  - name: Prod
    url: https://gaistnet.com/mon/health
    enabled: true
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

## Install

`sudo deploy/install.sh dev` or `sudo deploy/install.sh prod` does all of
this (build, install, config from `deploy/dev.yaml` or `deploy/prod.yaml` if
missing, service, nginx block with backup, test copy and
`/root/pods/nginx/start-nginx-container.sh`, health check) and can be run
again after changes. On Dev the block goes before the first `location /api/ {`,
on Prod before the first one in the server block that listens on 443.
Without go on the server, build elsewhere with
`CGO_ENABLED=0 go build -o tweedle ./cmd/tweedle` and copy `tweedle` into the
repo directory there; the script then uses it. By hand:

sudo on RHEL does not search `/usr/local/bin`, hence the full path.
`deploy/` holds the systemd unit, the Dev and Prod configs and the nginx block. With
`listen: podman:gaistnet:9300` Tweedle looks up the gateway of the podman
network `gaistnet` at start and listens there; the nginx container reaches it
as `host.containers.internal`, the LAN does not. The same config works on
every server whose nginx container is in that network.

```bash
CGO_ENABLED=0 go build -o tweedle ./cmd/tweedle
sudo install -m 755 tweedle /usr/local/bin/tweedle
sudo install -D -m 600 deploy/dev.yaml /etc/tweedle/config.yaml   # or prod.yaml
sudo /usr/local/bin/tweedle setup                     # choose processes and containers
sudo install -m 644 deploy/tweedle.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now tweedle
# nginx: add deploy/nginx-mon.conf to the server block, then (reload is not enough)
sudo /root/pods/nginx/start-nginx-container.sh
curl http://192.168.0.115/mon/health                 # Prod: https://gaistnet.com/mon/health
sudo /usr/local/bin/tweedle pair                      # QR code for Dee
```
