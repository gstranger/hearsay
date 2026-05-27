# Design: Status Subcommand & Cursor State Location

**Date:** 2026-05-26
**Scope:** Add a `hearsay status` command showing runtime state, and move Cursor state from `/tmp` to `~/.config/hearsay/cursor/`.

## 1. `hearsay status` Subcommand

### Motivation

No way to see what's happening at runtime without curling the REST API. A `status` command gives operators quick visibility.

### Design

```
hearsay status [--namespace <id>]
```

Output (simple text table):

```
Namespace:       default
Provider:        sqlite (.hearsay.db)
Active claims:   7
Mailbox:         12 messages (3 unread)
Agents online:   4
```

With `--verbose`:

```
Namespace:       default
Provider:        sqlite (.hearsay.db)
Active claims:   7
Mailbox:         12 messages (3 unread)
Agents online:   4 (agent-a, agent-b, agent-c, agent-d)

Active claims:
  agent-a   file://src/api.go     write     refactoring    2m ago
  agent-b   file://src/utils.go   delete    cleanup        5m ago

Recent mailbox (last 10):
  agent-c → agent-a   yield_request   1m ago
  agent-b → agent-a   note            3m ago
```

### Implementation

- New `cmdStatus` function in `cmd/hearsay/main.go`
- Queries provider via `ActiveClaims()` and `Query()` with recent offset
- Counts unread messages via `GetMailbox()` filtering
- No API changes needed — reuses existing provider methods

### Files
- `cmd/hearsay/main.go` — add `status` case, `cmdStatus` function

## 2. Cursor State Location

### Motivation

Cursor hook state is stored in `/tmp/hearsay-cursor-state.json` which is lost on reboot and not synced. Moving to `~/.config/hearsay/cursor/` follows platform conventions and persists state.

### Design

- **From:** `/tmp/hearsay-cursor-state.json`
- **To:** `<config-dir>/hearsay/cursor/state.json`
  - Linux: `~/.config/hearsay/cursor/state.json`
  - macOS: `~/Library/Application Support/hearsay/cursor/state.json`
  - Windows: `%APPDATA%\hearsay\cursor\state.json`
- Use `os.UserConfigDir()` for platform-independent path
- Create directory if not exists

### Files
- `cmd/hearsay/cursor.go` — update state path
- `cmd/hearsay/cursor_test.go` — update test paths

## Out of Scope
- JSON output for `status` (can add later)
- Multiple namespace status (one at a time)
- Real-time watch mode

## Verification
- `hearsay status` works with SQLite provider
- `hearsay status --verbose` shows detailed output
- Cursor state persists across reboots