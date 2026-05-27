# hearsay

[![Go Report Card](https://goreportcard.com/badge/github.com/gstranger/hearsay)](https://goreportcard.com/report/github.com/gstranger/hearsay)
[![GoDoc](https://godoc.org/github.com/gstranger/hearsay?status.svg)](https://godoc.org/github.com/gstranger/hearsay)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

> **Agent coordination layer** — track resource claims across agents, sessions, and machines. Now with [A2A (Agent-to-Agent)](https://github.com/google/A2A) protocol support.

`hearsay` is a lightweight, embeddable coordination service for AI agents. It prevents conflicts when multiple agents work on the same codebase by tracking who owns what, for how long, and why. With built-in A2A compliance, external agents can discover capabilities via an Agent Card and delegate coordination tasks through JSON-RPC.

---

## Features

- **🔒 Resource Claims** — Lock files, functions, or any URI with TTL-based expiration
- **📬 Mailbox** — Send typed messages between agents with claim correlation
- **⚡ Conflict Detection** — Check if an operation would conflict before executing
- **🌐 A2A Protocol** — Full compliance with Google's Agent-to-Agent protocol
  - Agent Card discovery (`GET /.well-known/agent.json`)
  - JSON-RPC 2.0 task delegation (`tasks/send`, `tasks/get`, `tasks/cancel`)
  - 5 built-in skills: claim/release resource, check conflict, query/send mailbox
- **🔐 Dual Auth** — API key (`X-Api-Key`) or Bearer token (JWT/JWKS + external validator)
- **💾 Multiple Backends** — SQLite (default), PostgreSQL, or managed remote provider
- **📦 Embeddable** — Use as a Go library or standalone binary
- **🖥️ IDE Integration** — Cursor IDE extension for real-time claim visualization

---

## Quick Start

### Installation

```bash
go install github.com/gstranger/hearsay/cmd/hearsay@latest
```

Or clone and build:

```bash
git clone https://github.com/gstranger/hearsay.git
cd hearsay
go build ./cmd/hearsay
```

### Initialize

```bash
hearsay init
```

Creates `.hearsay.toml`:

```toml
version = 1
namespace = "default"
provider = "sqlite"

[provider_config]
  [provider_config.sqlite]
    path = ".hearsay.db"

[defaults]
  ttl_seconds = 300
  claim_on_read = false
  auto_heartbeat = true
```

### Start the Server

```bash
# REST API only (default port 8080)
hearsay serve

# With A2A server on separate port
hearsay serve --a2a-addr localhost:8081 --a2a-api-key my-secret-key
```

### Claim a Resource

```bash
hearsay claim file://src/api.go --agent-id agent-a --operation write --intent "refactoring"
```

### Query Active Claims

```bash
hearsay query
```

### Release a Claim

```bash
hearsay release <claim-id> --outcome succeeded
```

---

## A2A Protocol

When started with `--a2a-addr`, hearsay exposes an A2A-compliant server.

### Agent Card

```bash
curl http://localhost:8081/.well-known/agent.json
```

Returns the [Agent Card](https://github.com/google/A2A/blob/main/documentation.md#agent-card) with 5 skills:

| Skill | Description |
|---|---|
| `claim_resource` | Lock a resource for exclusive access |
| `release_resource` | Release a previously granted claim |
| `check_conflict` | Check if an operation would conflict without claiming |
| `query_mailbox` | Read messages from an agent's mailbox |
| `send_mailbox` | Send a message to another agent's mailbox |

### JSON-RPC Example

```bash
curl -X POST http://localhost:8081/ \
  -H "Content-Type: application/json" \
  -H "X-Api-Key: my-secret-key" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tasks/send",
    "params": {
      "id": "task-1",
      "message": {
        "role": "user",
        "parts": [{
          "type": "data",
          "data": {
            "skill_id": "claim_resource",
            "resource_uri": "file://src/api.go",
            "operation": "write",
            "agent_id": "agent-a",
            "intent": "refactoring"
          }
        }]
      }
    }
  }'
```

Response:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "id": "task-1",
    "status": {
      "state": "completed",
      "timestamp": "2026-05-26T19:00:00Z"
    },
    "artifacts": [{
      "name": "claim-result",
      "parts": [{
        "type": "data",
        "data": {"claim_id": "...", "status": "granted"}
      }]
    }]
  }
}
```

### Authentication

| Scheme | Flag | Description |
|---|---|---|
| API Key | `--a2a-api-key` | Simple `X-Api-Key` header |
| JWT Bearer | `--a2a-bearer-jwks-url` | Validate JWT against JWKS endpoint |
| External Bearer | `--a2a-bearer-validator-url` | POST token to external validator |

---

## HTTP API

The REST API runs on the main port (default `:8080`) and is used by the TypeScript SDK, Cursor extension, and CLI.

| Endpoint | Method | Description |
|---|---|---|
| `/claims` | POST | Create a claim |
| `/claims/:id` | DELETE | Release a claim |
| `/claims` | GET | List active claims |
| `/conflict` | GET | Check for conflicts |
| `/mailbox` | POST | Send a message |
| `/mailbox` | GET | Query mailbox |

See [sdk/typescript/README.md](sdk/typescript/README.md) for TypeScript client usage.

---

## Configuration

### Environment Variables

| Variable | Description |
|---|---|
| `HEARSAY_NAMESPACE` | Default namespace |
| `HEARSAY_PROVIDER` | `sqlite`, `postgresql`, or `managed` |
| `HEARSAY_SQLITE_PATH` | SQLite database path |
| `HEARSAY_POSTGRESQL_URL` | PostgreSQL connection string |
| `HEARSAY_A2A_ADDR` | A2A server listen address |
| `HEARSAY_A2A_API_KEY` | A2A API key |

### TOML Config

```toml
version = 1
namespace = "production"
provider = "postgresql"

[provider_config]
  [provider_config.postgresql]
    url = "postgres://user:pass@localhost/hearsay"

[a2a]
  addr = "localhost:8081"
  api_key = "${HEARSAY_A2A_API_KEY}"  # read from env
  bearer_jwks_url = "https://auth.example.com/.well-known/jwks.json"

[defaults]
  ttl_seconds = 600
  claim_on_read = true
  auto_heartbeat = true
```

---

## Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│   Cursor IDE    │     │  TypeScript SDK │     │  External A2A   │
│   Extension     │     │   (REST API)    │     │    Agents       │
└────────┬────────┘     └────────┬────────┘     └────────┬────────┘
         │                       │                       │
         │ REST (:8080)          │ REST (:8080)          │ JSON-RPC (:8081)
         │                       │                       │
         └───────────────────────┼───────────────────────┘
                                 │
                    ┌────────────▼────────────┐
                    │      hearsay         │
                    │   (Go library/binary)   │
                    └────────────┬────────────┘
                                 │
              ┌──────────────────┼──────────────────┐
              │                  │                  │
        ┌─────▼─────┐    ┌──────▼──────┐   ┌──────▼──────┐
        │  SQLite   │    │ PostgreSQL  │   │   Managed   │
        │ (default) │    │             │   │  (remote)   │
        └───────────┘    └─────────────┘   └─────────────┘
```

---

## Development

```bash
# Run tests
go test ./...

# Run with race detection
go test -race ./...

# Build binary
go build ./cmd/hearsay

# Run locally
./hearsay serve --a2a-addr localhost:8081
```

### Project Structure

```
.
├── cmd/hearsay/          # CLI entrypoint
├── internal/
│   ├── a2a/                 # A2A protocol implementation
│   │   ├── agentcard.go     # Agent Card generator
│   │   ├── auth.go          # JWT + API key auth
│   │   ├── handler_*.go     # Task handlers & skills
│   │   ├── server.go        # HTTP server
│   │   └── task.go          # Task model & state machine
│   ├── sqlite/              # SQLite provider
│   ├── postgres/            # PostgreSQL provider
│   ├── managed/             # Remote provider client
│   ├── memory/              # In-memory provider (tests)
│   ├── server/              # REST HTTP handlers
│   └── mailbox/             # Mailbox implementation
├── pkg/hearsay/          # Public API types & client
├── sdk/
│   ├── typescript/          # TypeScript SDK
│   └── cursor/              # Cursor IDE extension
└── tests/integration/       # End-to-end tests
```

---

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feat/amazing-feature`)
3. Commit your changes (`git commit -m 'feat: add amazing feature'`)
4. Push to the branch (`git push origin feat/amazing-feature`)
5. Open a Pull Request

Please ensure:
- `go test ./...` passes
- New features include tests
- Code follows existing Go conventions

---

## License

MIT License — see [LICENSE](LICENSE) for details.

---

## Acknowledgments

- [Google A2A Protocol](https://github.com/google/A2A) — Agent-to-Agent interoperability standard
- Built with ❤️ for agents that need to coordinate
