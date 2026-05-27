# Agentstate Integration Guide

**Agentstate connects to your agent harness in one of three ways.** The key question is: **does your harness run TypeScript/JS or not?**

```
┌──────────────────────────────────────────────────────────────┐
│  TypeScript/JavaScript Harnesses                              │
│  Claude Code / Codex / pi                                     │
│  ↓ HTTP fetch()                                               │
│  Needs agentstate serve running (pi auto-starts it)          │
├──────────────────────────────────────────────────────────────┤
│  Go/CLI Harnesses                                             │
│  Cursor / Manual CLI / Watcher                                │
│  ↓ Direct SQLite/PostgreSQL (in-process)                      │
│  No server needed                                             │
└──────────────────────────────────────────────────────────────┘
```

---

## Why Some Need a Server and Some Don't

The `agentstate` binary is written in Go. It contains the claims protocol, provider interface, SQLite/PostgreSQL storage, and all the coordination logic.

**TypeScript can't import Go.** There's no way for `@agentstate/sdk` or the pi extension to open a SQLite file directly from Node.js. So we run `agentstate serve`, which exposes an HTTP API. TypeScript calls `POST /claim`, `GET /claims`, etc., and the Go binary handles storage.

**Go binaries don't need a server.** `agentstate claim`, `agentstate cursor`, `agentstate watch` are all Go commands. They open SQLite directly via the same Go library that the server uses.

| Harness | Language | Mechanism | Needs `serve`? | Auto-start? |
|---------|----------|-----------|---------------|-------------|
| **Claude Code** | TypeScript | `@agentstate/sdk` via HTTP | Yes | No — start manually |
| **Codex** | TypeScript | `@agentstate/sdk` via HTTP | Yes | No — start manually |
| **pi** | TypeScript | Extension API via HTTP | Yes | **Yes — auto-starts** |
| **OpenCode** | TypeScript | `tool.execute.before` hook | Yes | **Yes — auto-starts** |
| **Cursor** | Shell/Go | CLI `agentstate cursor` | No | N/A |
| **CLI** | Go | `agentstate claim/query` | No | N/A |
| **Watcher** | Go | `agentstate watch` | No | N/A |

---

## Claude Code / Codex

Claude Code and Codex support `PreToolUse` and `PostToolUse` hooks. The agent calls its normal tools; `@agentstate/sdk` intercepts transparently.

```typescript
import { createHooks } from "@agentstate/sdk";

const hooks = createHooks({
  endpoint: "http://localhost:8080",
  namespace: "org/repo/branch",
  agentId: "claude:claude-sonnet-4:sess_abc123",
  autoHeartbeat: true,
  defaultTTL: 300,
});

for await (const message of query({
  prompt: "Refactor auth",
  options: {
    hooks: {
      PreToolUse: [{ matcher: "Read|Write|Edit|Bash", hooks: hooks.preToolUse }],
      PostToolUse: [{ matcher: "Read|Write|Edit|Bash", hooks: hooks.postToolUse }],
      SessionStart: [{ hooks: hooks.sessionStart }],
      SessionEnd: [{ hooks: hooks.sessionEnd }],
    }
  }
})) { /* ... */ }
```

**Setup:**
1. Install the Go binary: `go install github.com/thunder/agentstate/cmd/agentstate@latest`
2. In your project directory: `agentstate init --provider sqlite --namespace org/repo/branch`
3. **Start the server in another terminal:** `agentstate serve`
4. `npm install @agentstate/sdk` and wire the hooks into your Claude Code harness

**What the agent sees:** Its normal `Read`, `Write`, `Edit`, `Bash` calls. On conflict, the hook returns `permissionDecision: "deny"` with a message like `Conflict: agent-7 is writing file://src/auth.ts`.

**Auto-heartbeat:** `autoHeartbeat: true` starts a timer that calls `POST /heartbeat` every 150 seconds, keeping claims alive during long operations.

---

## pi (Auto-Start Enabled)

pi has a native extension API: `pi.on("tool_call")` blocks tool execution and `pi.on("tool_result")` releases claims.

**Key difference from Claude Code:** The pi extension **auto-starts `agentstate serve`** in the background. You don't need a separate terminal.

```typescript
// .pi/extensions/agentstate.ts
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
// ... extension code auto-starts serve if not running ...
```

**Setup:**
1. Install the Go binary: `go install github.com/thunder/agentstate/cmd/agentstate@latest`
2. Copy `sdk/pi-extension/agentstate.ts` to `.pi/extensions/agentstate.ts`
3. Set environment variables (optional — defaults work):
   ```bash
   export AGENTSTATE_ENDPOINT=http://localhost:8080
   export AGENTSTATE_NAMESPACE=org/repo/branch
   ```
4. Start pi. The extension will:
   - Check if `localhost:8080` is running
   - If not: run `agentstate init` (if needed) → `agentstate serve &` in background
   - Then proceed with coordination

**What the agent sees:** On conflict, pi shows a "blocked" message with the conflicting agent's intent. The agent never sees the tool output for blocked calls.

---

## OpenCode (Block-With-Message)

OpenCode supports `tool.execute.before` hooks via plugins. Unlike pi, the hook API only supports **blocking** (throwing an error) — there's no native "warn but allow" return value.

**Setup:**
1. Install the Go binary: `go install github.com/thunder/agentstate/cmd/agentstate@latest`
2. Copy `sdk/opencode-extension/agentstate.ts` to `.opencode/plugins/agentstate.ts`
3. Set environment variables (optional — defaults work):
   ```bash
   export AGENTSTATE_ENDPOINT=http://localhost:8080
   export AGENTSTATE_NAMESPACE=org/repo/branch
   export AGENTSTATE_ON_CONFLICT=block  # block | allow (warn is experimental)
   ```
4. Start OpenCode. The plugin will:
   - Check if `localhost:8080` is running
   - If not: auto-start `agentstate serve`
   - Then intercept tool execution

**What the agent sees:** On conflict in `block` mode, the tool fails and the agent sees the error message: `Conflict: agent-X is write file://src/auth.ts (refactoring token validation)`.

**Conflict modes:**

| Mode | Behavior | Agent sees conflict? |
|------|----------|---------------------|
| `block` (default) | Throws error, tool fails | ✅ Yes — as error message |
| `allow` | Silently proceeds | ❌ No |
| `warn` | Proceeds, **attempts** to prepend warning to tool result | ⚠️ Experimental — may not reach agent |

**Limitation:** No auto-heartbeat. OpenCode hooks are per-call; there's no background timer. Long operations may expire.

---

## Cursor (No Server Needed)

Cursor supports a `hooks.json` file that runs shell commands before/after tool use. Coordination is done via the Go CLI directly — no HTTP server.

**Setup:**
1. Install the Go binary: `go install github.com/thunder/agentstate/cmd/agentstate@latest`
2. `agentstate init` in your project directory
3. Copy `sdk/cursor/hooks.json` to `.cursor/hooks.json`
4. **No `serve` needed.** Cursor subcommands open SQLite directly.

```json
{
  "version": 1,
  "hooks": {
    "sessionStart": [{ "command": "agentstate cursor session-start --session-id {{sessionId}} ...", "timeout": 5 }],
    "preToolUse": [{ "command": "agentstate cursor pre-tool-use --session-id {{sessionId}} --tool {{toolName}} ...", "timeout": 5, "matcher": "Read|Write|Edit|Bash" }],
    "postToolUse": [{ "command": "agentstate cursor post-tool-use --session-id {{sessionId}} --tool {{toolName}} ...", "timeout": 5, "matcher": "Read|Write|Edit|Bash" }],
    "sessionEnd": [{ "command": "agentstate cursor session-end --session-id {{sessionId}}", "timeout": 5 }]
  }
}
```

**What the agent sees:** On conflict, Cursor shows the `agent_message` from the CLI. The hook exits with code 2 (`deny`).

**Limitation:** No auto-heartbeat. Cursor's hooks are per-call; there's no background timer. Long operations may expire.

---

## Manual CLI (No Server Needed)

Any harness can use the CLI directly:

```bash
# Before editing
agentstate claim file://src/auth.ts --operation write --intent "refactoring token validation"

# Check before planning
agentstate check file://src/auth.ts --operation write
# → {"has_conflict":true,"conflicts":[...]}

# After editing
agentstate release <claim-id> --outcome succeeded
```

The CLI reads `.agentstate.toml` and opens SQLite/PostgreSQL directly.

---

## Filesystem Watcher Fallback (No Server Needed)

If your harness has no hook system:

```bash
agentstate watch --path ./src --namespace org/repo/branch --claim-ttl 60
```

This detects file changes and creates retroactive claims. Other agents see them via `agentstate query`.

---

## Adding Agentstate to a New Harness

| What your harness supports | Effort | See reference |
|-----------|--------|---------------|
| Pre/post tool callbacks (TypeScript) | ~80 lines | `sdk/typescript/src/hooks.ts` |
| Tool interception/blocking (TypeScript) | ~100 lines | `sdk/pi-extension/agentstate.ts` |
| `tool.execute.before` hook (TypeScript) | ~120 lines | `sdk/opencode-extension/agentstate.ts` |
| Shell commands around tool calls | ~150 lines | `cmd/agentstate/cursor.go` |
| No hooks | Zero code | `agentstate watch` or manual CLI |

---

## Quick Reference

### Start serve (TypeScript harnesses)
```bash
agentstate init --provider sqlite --namespace org/repo/branch
agentstate serve --addr localhost:8080
```

### Config (all harnesses)
```toml
# .agentstate.toml
version = 1
namespace = "org/repo/branch"
provider = "sqlite"

[provider_config.sqlite]
path = ".agentstate.db"
```

### Which binary do I install?
One binary: `go install github.com/thunder/agentstate/cmd/agentstate@latest`

It does everything: claims, queries, serves, watches, cursor hooks.

---

## Conflict Behavior

All integrations support three conflict modes. The default is `"warn"` — the tool proceeds but the agent sees an advisory message.

| Mode | Behavior |
|------|----------|
| `"warn"` (default) | Tool proceeds, agent sees `⚠️ Conflict: agent-X is write file://...` |
| `"block"` | Tool is rejected, agent sees conflict message |
| `"allow"` | Tool proceeds silently |

### Claude Code / Codex
```typescript
const hooks = createHooks({
  // ... other options ...
  onConflict: "warn",  // "block" | "warn" | "allow"
});
```

### pi
```bash
export AGENTSTATE_ON_CONFLICT=warn  # block | warn | allow
```

### Cursor
```bash
# In hooks.json:
"command": "agentstate cursor pre-tool-use ... --on-conflict warn"

# Or via environment:
export AGENTSTATE_ON_CONFLICT=warn
```

### OpenCode
OpenCode's hook API only supports blocking via `throw`. The `warn` mode attempts to prepend a warning to the tool result, but this is **experimental** and may not reach the agent. Default is `block`.

```bash
export AGENTSTATE_ON_CONFLICT=block  # block | allow (warn is experimental)
```

### Manual CLI & Watcher
Conflict modes only apply to integrations that intercept tool execution. The manual CLI and filesystem watcher report conflicts but do not block or warn automatically.

- **Manual CLI:** `agentstate check` returns conflict details as JSON; your script decides whether to proceed.
- **Watcher:** Creates retroactive claims after file changes are detected. Other agents see these via `agentstate query`.

---

## Mailbox Auto-Check

Agentstate's mailbox lets agents send messages to each other. **Critical messages are checked automatically** by supported harnesses before every tool execution.

### What gets checked automatically

| Message Type | Auto-checked? | Behavior |
|-------------|---------------|----------|
| `yield_request` | ✅ Yes | Tool blocked, agent sees message |
| `escalation` | ✅ Yes | Tool blocked, agent sees message |
| `note` | ❌ No | Check manually via skill |
| `yield_ack` | ❌ No | Check manually via skill |
| `all_clear` | ❌ No | Check manually via skill |
| `ping` | ❌ No | Check manually via skill |

### Harness support

| Harness | Auto-check support | How it works |
|---------|-------------------|--------------|
| **Claude Code / Codex** | ✅ | `preToolUse` checks mailbox, blocks on critical messages |
| **pi** | ✅ | `tool_call` event checks mailbox, blocks on critical messages |
| **OpenCode** | ✅ | `tool.execute.before` checks mailbox, throws on critical messages |
| **Cursor** | ❌ | No hook API for mailbox queries; check manually via skill |
| **Manual CLI** | ❌ | No auto-check; use `agentstate query` manually |

### What the agent sees

When a critical message is found:
- **Claude Code / Codex:** `permissionDecision: "deny"` with message `📬 Critical mailbox messages — you must respond before proceeding: ...`
- **pi:** `block: true` with reason `📬 Critical mailbox messages — you must respond before proceeding: ...`
- **OpenCode:** Error thrown with message `📬 Critical mailbox messages — you must respond before proceeding: ...`

### Manual checks

For routine messages (`note`, `yield_ack`, `all_clear`, `ping`), the coordination etiquette skill teaches agents to check manually:
- Before starting a new plan
- After completing work
- When a conflict is detected

```bash
# Check your mailbox manually
curl "http://localhost:8080/mailbox?namespace=org/repo/branch&agent_id=agent-A&unread=true"
```
