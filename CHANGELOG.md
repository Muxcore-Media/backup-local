# Changelog

## [0.1.2] — 2026-08-10

### Added

- Advertise `settings` capability so admin-ui discovers SettingsProvider without ListAll probing.

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.3] - 2026-10-05

### Changed
- CI runs on GitHub-hosted runners from the umbrella template; retired-origin workflows removed.
- Dependencies resolve from published GitHub tags (no filesystem `replace`); requires core v0.6.0.

### Security

- Enable TLS on the module gRPC listener by default via `internal/grpctls`; plaintext only when `MUXCORE_INSECURE_DISABLE_TLS=true` (or `MUXCORE_GRPC_INSECURE`).

## [0.1.1] — 2026-08-10

### Added

- `RegisterSettings` / `SettingsUpdater` for live `backup_dir` and `source_dirs`
- Pin `core` / contracts / `sdk/go/module` to **v0.5.2**

## [0.1.0] — 2026-08-09

### Added

- Real `.tar.gz` CreateBackup / RestoreBackup with SHA-256 index entries, source-dir archiving, optional Backupable ExportState/ImportState, and path-traversal rejection on restore
- Local `BackupService` proto (`proto/muxcore/backup/v1`)
- Dedicated Backupable-peer-only round-trip test + COMPATIBILITY peer-path notes

### Fixed

- CreateBackup no longer writes empty archives; RestoreBackup no longer returns `{status:ok}` without extracting
