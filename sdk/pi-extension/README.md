# pi Extension

Copy `hearsay.ts` to `.pi/extensions/hearsay.ts` (project-local) or `~/.pi/agent/extensions/hearsay.ts` (global).

## Requirements

The pi extension needs `hearsay` binary in your `$PATH`. Install with:

```bash
go install github.com/thunder/hearsay/cmd/hearsay@latest
```

## Auto-start behavior

The extension automatically starts `hearsay serve` in the background if:
- No server is running on the configured endpoint (default `localhost:8080`)
- The `hearsay` binary is found in `$PATH`

If `.hearsay.toml` does not exist, the extension auto-runs `hearsay init --provider sqlite --namespace <namespace>` first.

You can override the binary path:
```bash
export HEARSAY_BINARY=/path/to/hearsay
```

## Configuration

Set environment variables (optional — defaults shown):

```bash
export HEARSAY_ENDPOINT=http://localhost:8080
export HEARSAY_NAMESPACE=org/repo/branch
export HEARSAY_AGENT_ID=pi:gpt-4:sess_abc123
export HEARSAY_TTL=300
export HEARSAY_CLAIM_ON_READ=false
```

## Behavior

- Claims resources before `read`, `write`, `edit`, `bash` tool calls
- Blocks on conflict with advisory message
- Releases claims on tool result (succeeded or error)
- Releases all as "abandoned" on session shutdown
