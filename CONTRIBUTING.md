# Contributing to Your Module

## Development Setup

### Prerequisites

- Go 1.26.x
- golangci-lint (optional but recommended)

### Clone and build

```bash
git clone https://github.com/yourorg/your-module.git
cd your-module
make build
```

### Run against a local muxcored

```bash
# Terminal 1: start core in dev mode
cd ../core
MUXCORE_INSECURE_DISABLE_TLS=true ./muxcored

# Terminal 2: start module
make build && ./your-module --muxcore-mesh-addr localhost:9090
```

## Running Tests

```bash
make test
```

Tests must not depend on a running muxcored instance. Use mocks where needed.

## Linting

```bash
make lint
```

## Code Conventions

- No comments explaining what the code does — name things well instead.
- Comments only for non-obvious WHY — hidden invariants, workarounds.
- No `os.Exit` from library code — only `main` exits.
- Structured logging via `log/slog` — no `fmt.Println` in non-test code.
- Context propagation — every function that does I/O takes `ctx context.Context` as its first argument.
- Error wrapping — use `fmt.Errorf("operation %q: %w", name, err)`.

## Branch Naming

```
feat/<short-description>
fix/<short-description>
docs/<short-description>
refactor/<short-description>
```

## Pull Request Process

1. Branch from `main`.
2. Make your changes with tests.
3. Run `make ci` locally — it must pass.
4. Open a PR against `main`.
5. Squash-merge preferred.

## Security Vulnerabilities

Do **not** open a public issue. See [SECURITY.md](SECURITY.md) for the private reporting process.

## License

By contributing, you agree that your contributions will be licensed under the GPL-3.0 license.
