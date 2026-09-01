# Backup Local

[![CI](https://git.zem.systems/muxcore/backup-local/actions/workflows/ci.yml/badge.svg)](https://git.zem.systems/muxcore/backup-local/actions)
[![Go Version](https://img.shields.io/badge/Go-1.26-blue)](https://go.dev/)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)

**Local filesystem backup/restore provider.**

A MuxCore sidecar module that stores `.tar.gz` backups on the local filesystem and exposes create / restore / list / delete over gRPC (`BackupService`). Provides the `backup` / `backup.local` capabilities. Discovers mesh peers advertising `backupable` (e.g. `database-sqlite`) and includes their `ExportState` blobs in each archive.

---

## How It Works

```
CreateBackup ──→ collect source dirs + Backupable ExportState ──→ backups/<id>.tar.gz + index.json
RestoreBackup ──→ verify checksum ──→ staging extract ──→ atomic rename ──→ ImportState
```

**CreateBackup** builds a streaming gzip-compressed tar. Contents come from:

1. Configured source directories (`BACKUP_SOURCE_DIRS`) and/or per-request `source_paths` (allowlisted)
2. Registered `Backupable` peers (`modules/<id>/state.bin` via `ExportState`)

Default exclude globs skip incomplete downloads: `.incomplete`, `*.parts`, `*.part`, `*.!qb`, `*.tmp`.

Empty archives are rejected (`FailedPrecondition`). Each backup records size and SHA-256 in `index.json`. Optional AES-GCM encryption at rest (`BACKUP_ENCRYPT_KEY` or mesh `encryption-aesgcm` module).

**RestoreBackup** requires `target_path` under allowed roots. It refuses missing archives, checksum mismatches, and path-traversal entries. Extracts to a staging dir, imports module state, then atomically promotes the tree.

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
| `BACKUP_ALLOWED_ROOTS` | sources | Extra roots allowed for `source_paths` / `target_path` |
| `BACKUP_EXCLUDE` | see defaults | Comma-separated globs skipped during archive |
| `BACKUP_ENCRYPT_KEY` | _(empty)_ | 64-char hex AES-256 key for archive encryption |
| `BACKUP_SCHEDULE` | _(empty)_ | 5-field cron for automatic `CreateBackup` |
| `BACKUP_HTTP_ADDR` | `:9303` | HTTP listen (`/health`, `--health-check`) |
| gRPC listen | `:9302` | `BackupService` address |

Settings (admin-ui): `backup_dir`, `source_dirs`, `exclude_globs`, `max_backups`, `max_age_days`, `schedule_cron`.

---

## Quick Start

```bash
make build

export MUXCORE_INSECURE_DISABLE_TLS=true
export BACKUP_SOURCE_DIRS=/var/lib/muxcore/data
./backup-local --muxcore-mesh-addr localhost:9090
```

Docker health check: `CMD ["/module", "--health-check"]` probes `http://127.0.0.1:9303/health`.

---

## gRPC surface

| RPC | Behavior |
|-----|----------|
| `CreateBackup` | Write `.tar.gz`; return id, size, checksum, module ids |
| `RestoreBackup` | Staging extract + atomic apply into `target_path` |
| `ListBackups` | Index entries, newest first |
| `DeleteBackup` | Remove archive + index entry |

Proto: `proto/muxcore/backup/v1/backup.proto` (`BackupService`, `BackupableService`).

### grpcurl examples

Snapshot configured data dirs plus `database-sqlite` state (peer must advertise `backupable` on the mesh):

```bash
# Create backup including database-sqlite module state
grpcurl -plaintext -d '{"module_ids":["database-sqlite"]}' \
  127.0.0.1:9302 muxcore.backup.v1.BackupService/CreateBackup

# Create backup of extra allowlisted path (must be under BACKUP_SOURCE_DIRS / BACKUP_ALLOWED_ROOTS)
grpcurl -plaintext -d '{"source_paths":["/var/lib/muxcore/data"]}' \
  127.0.0.1:9302 muxcore.backup.v1.BackupService/CreateBackup

# List backups
grpcurl -plaintext -d '{}' \
  127.0.0.1:9302 muxcore.backup.v1.BackupService/ListBackups

# Restore into allowlisted target (extracts data/* and re-imports modules/*/state.bin)
grpcurl -plaintext -d '{"backup_id":"backup_123","target_path":"/var/lib/muxcore/restore"}' \
  127.0.0.1:9302 muxcore.backup.v1.BackupService/RestoreBackup

# Delete
grpcurl -plaintext -d '{"backup_id":"backup_123"}' \
  127.0.0.1:9302 muxcore.backup.v1.BackupService/DeleteBackup
```

---

## Capability

`backup` / `backup.local` — Local filesystem backup/restore

## License

GPL-3.0
