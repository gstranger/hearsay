# Design: Process Death Detection

**Date:** 2026-05-26
**Scope:** Auto-release claims from agents that stop sending heartbeats.

## Motivation

When an agent crashes holding a claim, that resource stays locked until TTL expires (up to 5 min). Death detection auto-releases those claims seconds after the last heartbeat, unblocking other agents.

## Design

### How It Works

1. **Heartbeat updates `last_seen`** — existing `POST /heartbeat` endpoint already touches the agent. We add a timestamp in the provider so `AgentState.LastSeen` returns the actual last heartbeat time.

2. **Background death detector** — new goroutine runs every 15 seconds. Queries `AgentState` per agent. If `now - last_seen > agent_timeout`, calls `provider.ReleaseClaim` for each active claim held by that agent, appending a release message with outcome `abandoned_by_death`.

3. **New CLI flag** — `--agent-timeout` (default 60 seconds, 0 = disabled). Configures how long without heartbeat before an agent is declared dead.

### Files

| File | Purpose |
|---|---|
| `cmd/hearsay/main.go` | Add `--agent-timeout` flag, death detector goroutine |
| `README.md` | Document flag |

**Note:** No provider changes needed — `AgentState.LastSeen` already reflects the latest message timestamp per agent (including heartbeats). |

### Out of Scope
- Heartbeat protocol change (existing `POST /heartbeat` is sufficient)
- Push notifications on death (agents poll claims to discover)
- Cross-instance detection (single server only)

## Verification
- Agent sends heartbeat → `last_seen` updates
- Agent stops heartbeat → after `agent_timeout`, claims auto-released with `abandoned_by_death`
- Agent_timeout=0 → death detection disabled (same goroutine, skips check)