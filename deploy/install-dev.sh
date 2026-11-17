#!/usr/bin/env bash
# Installs or updates Tweedle on Dev and adds /mon/ to nginx.
# Run from the repo:  sudo deploy/install-dev.sh
# Safe to run again: the config and an existing /mon/ block are kept.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
BIN=/usr/local/bin/tweedle
CONF=/etc/tweedle/config.yaml
UNIT=/etc/systemd/system/tweedle.service
NGINX_CONF=/root/pods/nginx/nginx.conf
NGINX_CONTAINER=image-server
NGINX_START=/root/pods/nginx/start-nginx-container.sh
HEALTH_URL=http://127.0.0.1/mon/health

step() { printf '\n== %s\n' "$*"; }
die() { printf 'FEHLER: %s\n' "$*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "bitte mit sudo starten: sudo $0"
cd "$REPO"
[[ -x "$NGINX_START" ]] || die "$NGINX_START fehlt"

step "Bauen"
if [[ -n "${SUDO_USER:-}" ]] && sudo -u "$SUDO_USER" bash -lc 'command -v go' >/dev/null; then
  sudo -u "$SUDO_USER" bash -lc "cd '$REPO' && go build -o tweedle ./cmd/tweedle"
  echo "gebaut als $SUDO_USER"
elif [[ -x ./tweedle ]]; then
  echo "kein go gefunden, nehme das vorhandene ./tweedle"
else
  die "kein ./tweedle und kein go: erst 'go build -o tweedle ./cmd/tweedle'"
fi

step "Programm nach $BIN"
install -m 755 ./tweedle "$BIN"
"$BIN" version

step "Config $CONF"
if [[ -f "$CONF" ]]; then
  echo "vorhanden, bleibt wie sie ist"
else
  install -D -m 600 deploy/dev.yaml "$CONF"
  echo "angelegt aus deploy/dev.yaml - jetzt 'sudo $BIN setup' ausführen und das Skript erneut starten"
  exit 0
fi

step "systemd-Dienst"
install -m 644 deploy/tweedle.service "$UNIT"
systemctl daemon-reload
systemctl enable tweedle >/dev/null
systemctl restart tweedle
sleep 2
if ! systemctl is-active --quiet tweedle; then
  journalctl -u tweedle -n 20 --no-pager
  die "Dienst läuft nicht (Log oben)"
fi
echo "läuft"

step "nginx /mon/"
if grep -q 'location /mon/' "$NGINX_CONF"; then
  echo "Block schon vorhanden"
else
  backup="$NGINX_CONF.bak-mon-$(date +%Y%m%d-%H%M%S)"
  cp -a "$NGINX_CONF" "$backup"
  echo "Sicherung: $backup"
  tmp="$(mktemp)"
  # Insert the block (indented) before the first "location /api/ {" and the
  # comment lines directly above it.
  LC_ALL=C awk -v blk="$REPO/deploy/nginx-mon.conf" '
    BEGIN { while ((getline l < blk) > 0) b = b (l == "" ? "" : "        " l) "\n" }
    /^[[:space:]]*#/ { pend = pend $0 "\n"; next }
    !done && /^[[:space:]]*location \/api\/ \{/ { printf "%s\n", b; done = 1 }
    { printf "%s", pend; pend = ""; print }
    END { printf "%s", pend; if (!done) exit 3 }
  ' "$NGINX_CONF" > "$tmp" || { rm -f "$tmp"; die "'location /api/ {' nicht gefunden, nginx.conf unverändert"; }
  # Test a copy inside the running container first (relative includes made absolute).
  sed 's|include mime.types;|include /usr/local/openresty/nginx/conf/mime.types;|' "$tmp" |
    podman exec -i "$NGINX_CONTAINER" sh -c 'cat > /tmp/mon-test.conf'
  if ! podman exec "$NGINX_CONTAINER" openresty -t -c /tmp/mon-test.conf; then
    podman exec "$NGINX_CONTAINER" rm -f /tmp/mon-test.conf
    rm -f "$tmp"
    die "nginx-Test fehlgeschlagen, nginx.conf unverändert"
  fi
  podman exec "$NGINX_CONTAINER" rm -f /tmp/mon-test.conf
  cat "$tmp" > "$NGINX_CONF"
  rm -f "$tmp"
  echo "eingebaut, starte nginx neu"
  # The config only takes effect through the start script (reload is not enough).
  "$NGINX_START"
fi

step "Test $HEALTH_URL"
for _ in $(seq 1 20); do
  if out="$(curl -fsS -m 3 "$HEALTH_URL" 2>/dev/null)"; then
    echo "$out"
    printf '\nFertig. Koppeln mit Dee:  sudo %s pair\n' "$BIN"
    exit 0
  fi
  sleep 1
done
journalctl -u tweedle -n 10 --no-pager
die "keine Antwort von $HEALTH_URL"
