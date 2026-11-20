# Tweedle

Monitoring agent for Linux servers (Go). Watches system resources, selected
processes, Podman containers, TLS certificates and other servers, and reports
problems as events (info, warn, error). The app **Dee** (Android, iOS) shows
the state and receives push messages.

## Install

Needs Linux with systemd; building needs Go 1.22 or newer. The binary is
static, so it can also be built on another machine and copied over.

```bash
git clone https://github.com/impiperl/Tweedle && cd Tweedle
CGO_ENABLED=0 go build -o tweedle ./cmd/tweedle
sudo ./tweedle install
```

`install` asks a few questions (Enter takes the suggestion), then copies
itself to `/usr/local/bin/tweedle`, writes `/etc/tweedle/config.yaml`, sets up
and starts the systemd service `tweedle` and checks that it answers. Run it
again at any time to change the settings; the current values are suggested.

1. **Name** of the server, shown in the app and in push messages.
2. **How the app reaches the server:**
   - **Through a web server** already running (nginx, Apache, Caddy …),
     recommended: Tweedle listens on `127.0.0.1:PORT`, you enter the public
     address (e.g. `https://example.com/tweedle`), and `install` prints the
     lines to add for nginx, Apache and Caddy. It does not edit the web
     server config itself.
   - **Directly over HTTPS:** port, domain and certificate files (Let's
     Encrypt paths are suggested if present). Renewed certificates are picked
     up without a restart. Open the port in the firewall.
   - **Directly without encryption:** only on a home network or VPN.
3. **Other Tweedle servers to watch** (optional): enter their address; see
   [Two servers watching each other](#two-servers-watching-each-other).
4. Whether to **watch the certificate** of the public address (HTTPS only).
5. **Processes and containers** to watch (selection list, also available
   later as `sudo tweedle setup`).

Then pair the app: `sudo tweedle pair` shows a QR code (and a code to type),
valid for 10 minutes and once. In Dee: "Add server", scan.

If `sudo` cannot find `tweedle` (e.g. RHEL, where `/usr/local/bin` is not in
sudo's path), use `sudo /usr/local/bin/tweedle …`.

### Commands

```bash
sudo tweedle install    # set up or change (questions, config, service)
sudo tweedle setup      # choose processes and containers
sudo tweedle status     # current state once
sudo tweedle pair       # QR code + code to pair the Dee app (single use, 10 min)
sudo tweedle devices    # list paired devices; "devices rm ID" removes one
sudo tweedle testpush   # send a test push to all devices
tweedle serve           # run the checks and the API (done by the service)
```

`--config PATH` uses another config file. After editing the config by hand:
`sudo systemctl restart tweedle`.

### Web server in a container

If the web server runs in a Podman container, `listen: podman:NETWORK:PORT`
makes Tweedle listen on the gateway of that Podman network (looked up at
start); the container reaches it as `host.containers.internal:PORT`, the
outside does not. When podman is installed, `install` asks for that network
(empty = no) and prints the matching web server lines.

### Remove

```bash
sudo systemctl disable --now tweedle
sudo rm /etc/systemd/system/tweedle.service /usr/local/bin/tweedle
sudo rm -r /etc/tweedle /var/lib/tweedle
```

## Two servers watching each other

Every Tweedle answers `GET /health` without login. Install Tweedle on both
servers and, when `install` asks for other servers, enter the address of the
other one (e.g. `https://b.example.com/tweedle`). Each server then checks the
other every interval and raises an event, and a push message, when it stops
answering. With one Dee app paired to both, you hear about a failed server
from the one that is still up.

## Endpoints

`endpoints:` in the config lists URLs that must answer (`install` adds other
Tweedle servers here); only entries with `enabled: true` are checked. Each
tick every enabled URL is requested once (new connection each time). Anything
below HTTP 400 counts as up, or exactly `status` if set; a timeout (default
10s), connection or TLS error raises `endpoint_down` (error), an answer
slower than `slow` (if set) `endpoint_slow` (warn). Any web address works,
not only Tweedle servers:

```yaml
endpoints:
  - name: b.example.com
    url: https://b.example.com/tweedle/health
    enabled: true
  - name: Shop
    url: https://shop.example.com/
    enabled: true
    slow: 2s
```

## Push

Events go through the Expo Push Service (outgoing HTTPS only). Each device
gets the levels it chose (`minLevel`: info, warn, error, none), in its own
language, on the Android channel `info`, `warn` or `error` (the app creates
these channels). Events within `push.debounce` (3s) are collected; more than
three for one device become one summary message. Tokens Expo reports as
`DeviceNotRegistered` are removed. `push.access_token` is only needed if
enhanced push security is on in the Expo account.

## Container logs

Dee can show the logs of the watched containers (only those listed under
`containers:`), filtered by level (all, warnings, errors), text and time
range. Multi-line entries such as Java stack traces stay together; an entry
counts as error or warning if any of its lines does (ERROR, FATAL, SEVERE,
panic, Traceback, `…Exception`, `…Error`; WARN, WARNING).

**Caution:** every paired device can read these logs. If passwords, keys or
other secrets are written to them, the app shows them. Tweedle always hides
mail addresses, passwords in URLs (`user:***@host`), JWTs, `Bearer …`
tokens and the values of keys containing `password`, `passwd`, `pwd`,
`secret`, `token`, `apikey`, `api_key`, `api-key`, `authorization`,
`credential`, `private_key` or `cookie` (`key=value`, `key: value`,
`"key": "value"`). Add more key names, or switch logs off:

```yaml
logs:
  enabled: true          # false: the app cannot read any logs
  redact_keys: [session, iban]
```

`install` asks about it when containers are watched.

## API

Routes start at `/`; a web server in front forwards its path (e.g.
`/tweedle/`) with the prefix stripped. Everything except `/health` and
`/api/pair` needs `Authorization: Bearer <token>` (token comes from pairing).

| Route | |
|---|---|
| `GET /health` | `{ok, name, version}` |
| `POST /api/pair` | `{code, name, lang, pushToken?}` → `{token, deviceId, name}` |
| `GET /api/status` | current snapshot + unseen warn/error count, `logs`: whether container logs can be read |
| `GET /api/history?range=1h\|6h\|24h\|7d` | minute values (max. 360 points) |
| `GET /api/events?before=ID&limit=50&minLevel=warn` | events, newest first, `message` in the device language |
| `POST /api/events/seen` | `{ids: [ID, …]}`: the events the app has shown (per device; `{upTo: ID}` still works) |
| `GET/PUT/DELETE /api/device` | own settings (`name, lang, pushToken, minLevel`), unpair |
| `GET /api/containers/NAME/logs?since=1h&level=error&q=text&limit=300` | logs of a watched container, oldest first: `{entries: [{time, level, text}], truncated}`; `since` 15m, 1h, 6h, 24h, 7d or all; 403 if `logs.enabled` is false, 404 if not watched |
| `GET /api/live` | WebSocket: `{type:"status"}` every tick, `{type:"event"}` per event |

`--config PATH` uses another config file. For testing as a normal user,
`podman_cmd: [sudo, -n, podman]` in the config reads the root containers.
