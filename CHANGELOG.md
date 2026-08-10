# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] — 2026-08-09

### Added

- Real `.tar.gz` CreateBackup / RestoreBackup with SHA-256 index entries, source-dir archiving, optional Backupable ExportState/ImportState, and path-traversal rejection on restore
- Local `BackupService` proto (`proto/muxcore/backup/v1`)
- Dedicated Backupable-peer-only round-trip test + COMPATIBILITY peer-path notes

### Fixed

- CreateBackup no longer writes empty archives; RestoreBackup no longer returns `{status:ok}` without extracting
