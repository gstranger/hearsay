# Design: Rename agentstate → hearsay

**Date:** 2026-05-26
**Scope:** Purely mechanical rename — zero behavior changes.

## Motivation

The repository is published at `github.com/gstranger/hearsay` but internally still uses the old name `agentstate` everywhere — module path, binary, config file, env vars, package names, and user-facing strings. This rename aligns all internal identifiers with the public repository name.

## Changes

### 1. Go Module Path

- **From:** `github.com/thunder/agentstate`
- **To:** `github.com/gstranger/hearsay`
- **Files:** `go.mod`, all `import` statements across ~61 Go files

### 2. Binary & CLI

- **Directory:** `cmd/agentstate/` → `cmd/hearsay/`
- **Binary name:** `agentstate` → `hearsay`
- **CLI strings:** All help text, flag descriptions, error messages updated

### 3. Config File

- **From:** `.agentstate.toml`
- **To:** `.hearsay.toml`
- **Files:** Config generation, file watcher, docs

### 4. Environment Variables

- **Prefix:** `AGENTSTATE_*` → `HEARSAY_*`
- **Examples:**
  - `AGENTSTATE_NAMESPACE` → `HEARSAY_NAMESPACE`
  - `AGENTSTATE_PROVIDER` → `HEARSAY_PROVIDER`
  - `AGENTSTATE_A2A_ADDR` → `HEARSAY_A2A_ADDR`

### 5. Package Names

- **Directory:** `pkg/agentstate/` → `pkg/hearsay/`
- **Package name:** `agentstate` → `hearsay` (where used as package identifier)

### 6. String References

All user-facing strings containing "agentstate" become "hearsay":
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
