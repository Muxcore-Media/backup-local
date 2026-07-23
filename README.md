# Backup Local

[![CI](https://github.com/Muxcore-Media/backup-local/actions/workflows/ci.yml/badge.svg)](https://github.com/Muxcore-Media/backup-local/actions)
[![Go Version](https://img.shields.io/badge/Go-1.26-blue)](https://go.dev/)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)

**Local filesystem backup/restore provider.**

A MuxCore sidecar module that stores `.tar.gz` backups on the local filesystem and exposes create / restore / list / delete over gRPC (`BackupService`). Provides the `backup` / `backup.local` capabilities.

---

## How It Works

```
CreateBackup ──→ collect source dirs + Backupable ExportState ──→ backups/<id>.tar.gz + index.json
RestoreBackup ──→ verify checksum ──→ safe untar into target_path ──→ optional ImportState
```

**CreateBackup** builds a real gzip-compressed tar. Contents come from:

1. Configured source directories (`BACKUP_SOURCE_DIRS`) and/or per-request `source_paths`
2. Registered `Backupable` peers (`modules/<id>/state.bin` via `ExportState`)

Empty archives are rejected (`FailedPrecondition`). Each backup records size and SHA-256 in `index.json`.

**RestoreBackup** requires `target_path`. It refuses missing archives, checksum mismatches, and path-traversal entries (`../`). After a safe extract, registered peers whose state is present in the archive receive `ImportState`.

Archive layout:

```
data/<source-name>/…     # filesystem sources
modules/<id>/state.bin   # Backupable exports
```

---

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `BACKUP_DIR` | `backups` | Directory for archives and `index.json` |
| `BACKUP_SOURCE_DIRS` | _(empty)_ | Comma-separated directories included in every create |
| gRPC listen | `:9302` | `BackupService` address |
| HTTP listen | `:9303` | `/health` |

---

## Quick Start

```bash
make build

export MUXCORE_INSECURE_DISABLE_TLS=true
export BACKUP_SOURCE_DIRS=/var/lib/muxcore/data
./backup-local --muxcore-mesh-addr localhost:9090
```

---

## gRPC surface

| RPC | Behavior |
|-----|----------|
| `CreateBackup` | Write `.tar.gz`; return id, size, checksum, module ids |
| `RestoreBackup` | Untar into `target_path`; error if archive missing |
| `ListBackups` | Index entries, newest first |
| `DeleteBackup` | Remove archive + index entry |

Proto: `proto/muxcore/backup/v1/backup.proto`.

---

## Capability

`backup` / `backup.local` — Local filesystem backup/restore

## License

GPL-3.0
