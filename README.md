# Backup Local

[![CI](https://github.com/Muxcore-Media/backup-local/actions/workflows/ci.yml/badge.svg)](https://github.com/Muxcore-Media/backup-local/actions)
[![Go Version](https://img.shields.io/badge/Go-1.26-blue)](https://go.dev/)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)

**Local filesystem backup/restore provider.**

A MuxCore sidecar module that stores backups on the local filesystem and exposes create / restore / list / delete over gRPC. Provides the `backup` capability.

---

## How It Works

```
Module request ──→ backup-local (gRPC) ──→ backups/ (local dir)
```

Backups are written under `BACKUP_DIR` with an on-disk index. Restore copies a named backup back to the requested target path.

---

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `BACKUP_DIR` | `backups` | Directory for backup archives and index |
| gRPC listen | `:9302` | Backup service gRPC address |
| HTTP listen | `:9303` | Health/HTTP listen address |

---

## Quick Start

```bash
make build

export MUXCORE_INSECURE_DISABLE_TLS=true
./backup-local --muxcore-mesh-addr localhost:9090
```

---

## Capability

`backup` — Local filesystem backup/restore

## License

GPL-3.0
