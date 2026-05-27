# OpenCode Extension

Copy `hearsay.ts` to `.opencode/plugins/hearsay.ts` (project-local) or `~/.config/opencode/plugins/hearsay.ts` (global).

## Requirements

The extension needs `hearsay` binary in your `$PATH`. Install with:

```bash
go install github.com/thunder/hearsay/cmd/hearsay@latest
```

## Auto-start behavior

The extension automatically starts `hearsay serve` in the background if:
- No server is running on the configured endpoint (default `localhost:8080`)
- The `hearsay` binary is found in `$PATH`

If `.hearsay.toml` does not exist, the extension auto-runs `hearsay init --provider sqlite --namespace <namespace>` first.

## Configuration

Set environment variables (optional — defaults shown):

```bash
export HEARSAY_ENDPOINT=http://localhost:8080
export HEARSAY_NAMESPACE=org/repo/branch
export HEARSAY_AGENT_ID=opencode:gpt-4:sess_abc123
export HEARSAY_TTL=300
export HEARSAY_CLAIM_ON_READ=false
export HEARSAY_ON_CONFLICT=block  # block | warn | allow
```

## Conflict Modes

OpenCode's `tool.execute.before` hook API differs from pi's. Here's what each mode actually does:

| Mode | Behavior | Agent sees conflict? |
|------|----------|---------------------|
| **`block`** (default) | Throws error, tool fails | ✅ Yes — as error message |
| **`allow`** | Silently proceeds | ❌ No |
| **`warn`** | Proceeds, **attempts** to prepend warning to tool result | ⚠️ Experimental (see below) |

### Why `warn` is experimental

OpenCode's `tool.execute.after` hook receives `(input, output)` but the docs don't confirm whether `output` is mutable or where tool results actually flow. The extension attempts to prepend a warning to `output.result`:

```javascript
"tool.execute.after": async (input, output) => {
  output.result = `⚠️ Conflict: ...\n\n---\n${output.result}`;
}
```

**This may or may not reach the agent.** If you test this and confirm it works (or doesn't), please open an issue.

### Why `block` is the default

Since `warn` is unconfirmed, `block` is the safest default. The agent sees the conflict as an error and can choose a different action. This is actually more protective than a warning the agent might ignore.

## Behavior

- Claims resources before `read`, `write`, `edit`, `bash` tool calls
- Blocks on conflict (in `block` mode)
- Releases claims on tool result
- Releases all as "abandoned" on session shutdown/idle (`session.status` event)
