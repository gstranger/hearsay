# agentstate

Agent coordination layer — track resource claims across agents, sessions, and machines.

## Build

```bash
cd /Users/pj/Documents/thunder/agentstate
go build ./cmd/agentstate
```

## Test

```bash
go test ./...
```

## Commands

- `agentstate init` — create `.agentstate.toml`
- `agentstate claim` — claim a resource
- `agentstate release` — release a claim
- `agentstate query` — list active claims
- `agentstate serve` — start HTTP API server
