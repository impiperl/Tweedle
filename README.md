# Tweedel

Monitoring agent for Linux servers (Go). Watches selected processes, system
resources and Podman containers and reports problems as events (info, warn,
error). The app "Dee" shows the state and receives push messages.

Plan and background: wiki, Tools → "Tweedel & Dee (Monitoring)".

```bash
go build -o tweedel ./cmd/tweedel   # build
go test ./...                       # tests

sudo ./tweedel setup     # choose what to watch → /etc/tweedel/config.yaml
sudo ./tweedel status    # current state once
sudo ./tweedel serve     # run the checks (systemd service)
```

`--config PATH` uses another config file. For testing as a normal user,
`podman_cmd: [sudo, -n, podman]` in the config reads the root containers.
