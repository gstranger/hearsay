# Design: Rename hearsay → hearsay

**Date:** 2026-05-26
**Scope:** Purely mechanical rename — zero behavior changes.

## Motivation

The repository is published at `github.com/gstranger/hearsay` but internally still uses the old name `hearsay` everywhere — module path, binary, config file, env vars, package names, and user-facing strings. This rename aligns all internal identifiers with the public repository name.

## Changes

### 1. Go Module Path

- **From:** `github.com/thunder/hearsay`
- **To:** `github.com/gstranger/hearsay`
- **Files:** `go.mod`, all `import` statements across ~61 Go files

### 2. Binary & CLI

- **Directory:** `cmd/hearsay/` → `cmd/hearsay/`
- **Binary name:** `hearsay` → `hearsay`
- **CLI strings:** All help text, flag descriptions, error messages updated

### 3. Config File

- **From:** `.hearsay.toml`
- **To:** `.hearsay.toml`
- **Files:** Config generation, file watcher, docs

### 4. Environment Variables

- **Prefix:** `HEARSAY_*` → `HEARSAY_*`
- **Examples:**
  - `HEARSAY_NAMESPACE` → `HEARSAY_NAMESPACE`
  - `HEARSAY_PROVIDER` → `HEARSAY_PROVIDER`
  - `HEARSAY_A2A_ADDR` → `HEARSAY_A2A_ADDR`

### 5. Package Names

- **Directory:** `pkg/hearsay/` → `pkg/hearsay/`
- **Package name:** `hearsay` → `hearsay` (where used as package identifier)

### 6. String References

All user-facing strings containing "hearsay" become "hearsay":
- Log messages
- HTTP headers and error responses
- A2A Agent Card name/description
- README, documentation, comments
- TypeScript SDK strings
- Cursor extension metadata

## Verification

1. `go mod tidy` — clean module graph
2. `go test ./...` — all tests pass
3. `go build ./cmd/hearsay` — produces working binary
4. Integration tests pass

## Rollback

Single git commit. Revert with `git revert <commit>` if issues arise.

## Out of Scope

- No behavior changes
- No API changes (REST endpoints, A2A methods remain identical)
- No database schema changes
- No new features
