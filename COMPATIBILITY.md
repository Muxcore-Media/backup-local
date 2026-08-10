# Compatibility

## Core Version

| Module Version | Core Version | Status |
|----------------|-------------|--------|
| v0.1.0         | v0.4.0+     | Current |

## Contracts

| Contract | Capability | Status |
|----------|-----------|--------|
| BackupService (local proto) | `backup` | Current |
| `contracts.Backupable` peers | ExportState / ImportState | Optional inputs to Create/Restore |

## Backupable peer path

`CreateBackup` can archive:

1. `source_paths` / `BACKUP_SOURCE_DIRS` filesystem trees under `data/<basename>/…`
2. Registered **Backupable** peers → `modules/<id>/state.bin` via `ExportState`

`RestoreBackup` extracts into `target_path`, then calls `ImportState` on registered peers whose state is present in the archive. Peers that were not registered at restore time are skipped (files remain on disk under `modules/`).

Unit coverage: `TestBackupablePeerOnlyRoundTrip` and `TestRoundTripCreateListRestoreDelete` exercise Export → archive → Import without a live muxcored mesh.

## Breaking Changes

This is a pre-1.0 module. Interfaces may change without notice.
