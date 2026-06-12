# Your Module

[![CI](https://github.com/yourorg/your-module/actions/workflows/ci.yml/badge.svg)](https://github.com/yourorg/your-module/actions)
[![Go Version](https://img.shields.io/badge/Go-1.26-blue)](https://go.dev/)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)

**One-line description of what your module does.**

A MuxCore sidecar module that does X. Without this module, core can't do Y.

---

## How It Works

```
Client request ──→ your-module ──→ muxcored
                     │
                     ▼
              Does the thing
```

### Key concept 1

Explanation.

### Key concept 2

Explanation.

---

## Configuration

### CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--flag-name` | value | Description |

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `YOUR_MODULE_ADDR` | `:9400` | Listen address |

---

## Quick Start

```bash
# Build
make build

# Run against local core (dev mode)
export MUXCORE_INSECURE_DISABLE_TLS=true
./your-module --muxcore-mesh-addr localhost:9090
```

---

## Deployment

### Docker

```bash
make docker
docker run -d --restart=unless-stopped \
  -e MUXCORE_GRPC_ADDR=core:9090 \
  ghcr.io/yourorg/your-module:latest
```

### docker-compose

```bash
docker compose -f deploy/docker-compose.yml up
```

### systemd

```bash
sudo cp deploy/systemd/muxcore-module.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now muxcore-module
```

---

## Development

```bash
make dev      # run in dev mode
make test     # run tests
make lint     # golangci-lint
make fmt      # format code
```

### Integration Tests

```bash
# Start core in dev mode, then:
MUXCORE_GRPC_ADDR=localhost:9090 go test -tags=integration -race -count=1 ./test/
```

---

## Implementation

- Registers with capabilities: `"your.capability"`
- Implements `contracts.YourContract`
- Uses `contracts.DatabaseProvider` for persistence

---

## License

GPL-3.0
