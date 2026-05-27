# pi Extension

Copy `agentstate.ts` to `.pi/extensions/agentstate.ts` (project-local) or `~/.pi/agent/extensions/agentstate.ts` (global).

## Requirements

The pi extension needs `agentstate` binary in your `$PATH`. Install with:

```bash
go install github.com/thunder/agentstate/cmd/agentstate@latest
```

## Auto-start behavior

The extension automatically starts `agentstate serve` in the background if:
- No server is running on the configured endpoint (default `localhost:8080`)
- The `agentstate` binary is found in `$PATH`

If `.agentstate.toml` does not exist, the extension auto-runs `agentstate init --provider sqlite --namespace <namespace>` first.

You can override the binary path:
```bash
export AGENTSTATE_BINARY=/path/to/agentstate
```

## Configuration

Set environment variables (optional — defaults shown):

```bash
export AGENTSTATE_ENDPOINT=http://localhost:8080
export AGENTSTATE_NAMESPACE=org/repo/branch
export AGENTSTATE_AGENT_ID=pi:gpt-4:sess_abc123
export AGENTSTATE_TTL=300
export AGENTSTATE_CLAIM_ON_READ=false
```

## Behavior

- Claims resources before `read`, `write`, `edit`, `bash` tool calls
- Blocks on conflict with advisory message
- Releases claims on tool result (succeeded or error)
- Releases all as "abandoned" on session shutdown
