// Package manifest embeds muxcore.json so the module's reported version has a
// single source (ADR-0021).
package manifest

import _ "embed"

// ManifestJSON is the raw muxcore.json; its "version" is the module's only
// version source (ADR-0021). Read it with modulesdk.ManifestVersion.
//
//go:embed muxcore.json
var ManifestJSON []byte
