# Process Death Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Auto-release claims from agents that stop sending heartbeats within a configurable timeout.

**Architecture:** Add a death detector goroutine in `cmdServe` that checks `AgentState.LastSeen` every 15s. If an agent hasn't been seen in `--agent-timeout` seconds, auto-release all their claims with outcome `abandoned_by_death`.

**Tech Stack:** Go 1.25 standard library

---

### File Mapping

| File | Responsibility |
|---|---|
| `cmd/hearsay/main.go` | Add `--agent-timeout` flag, death detector goroutine |
| `README.md` | Document flag |

---

### Task 1: Add Death Detector to cmdServe

**Files:**
- Modify: `cmd/hearsay/main.go`

- [ ] **Step 1: Add `--agent-timeout` flag**

In `cmdServe`, after existing flags:

```go
agentTimeout := fs.Int("agent-timeout", 60, "Seconds without heartbeat before agent is declared dead (0 = disabled)")
```

- [ ] **Step 2: Add death detector goroutine**

After the existing sweeper goroutine (which runs `ReleaseExpired` every 60s), add:

```go
// Death detector: auto-release claims from dead agents
go func() {
	if *agentTimeout <= 0 {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now().UTC()
			claims, err := provider.ActiveClaims(context.Background(), cfg.Namespace, "")
			if err != nil {
				log.Printf("death detector error: %v", err)
				continue
			}

			// Group claims by agent
			agentClaims := make(map[string][]hearsay.Claim)
			for _, c := range claims {
				agentClaims[c.AgentID] = append(agentClaims[c.AgentID], c)
			}

			for agentID, agentClaims := range agentClaims {
				state, err := provider.AgentState(context.Background(), cfg.Namespace, agentID)
				if err != nil {
					continue
				}

				if state.LastSeen.IsZero() {
					continue
				}

				if now.Sub(state.LastSeen) > time.Duration(*agentTimeout)*time.Second {
					log.Printf("agent %s appears dead (last seen %s ago), releasing %d claims",
						agentID, now.Sub(state.LastSeen).Truncate(time.Second), len(agentClaims))

					for _, c := range agentClaims {
						payload, _ := json.Marshal(map[string]string{
							"claim_id": c.ClaimID,
							"outcome":  "abandoned_by_death",
						})
						msg := hearsay.Message{
							Type:    hearsay.MsgRelease,
							AgentID: agentID,
							Payload: payload,
						}
						if err := provider.Append(context.Background(), cfg.Namespace, []hearsay.Message{msg}); err != nil {
							log.Printf("death detector: failed to release claim %s: %v", c.ClaimID, err)
						}
					}
				}
			}
		}
	}
}()
```

- [ ] **Step 3: Build and test**

```bash
cd ~/Documents/agentstate
go build ./cmd/hearsay
go test ./...
```

Expected: builds, all 11 packages pass

- [ ] **Step 4: Commit**

```bash
git add cmd/hearsay/main.go
git commit -m "feat: add process death detection with --agent-timeout flag"
```

---

### Task 2: Update README and Verify

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add flag to docs**

Add to server examples:
```bash
# With 30s death detection timeout
hearsay serve --agent-timeout 30
```

Add to env var table:
```
| `HEARSAY_AGENT_TIMEOUT` | Seconds without heartbeat before agent declared dead (0 = disabled) |
```

- [ ] **Step 2: Run tests and merge**

```bash
cd ~/Documents/agentstate
go test ./...
go build ./cmd/hearsay
git add README.md
git commit -m "docs: document --agent-timeout flag"
```

---

## Spec Coverage Check

| Spec Requirement | Task |
|---|---|
| `--agent-timeout` flag | Task 1 |
| Death detector goroutine | Task 1 |
| Auto-release with `abandoned_by_death` | Task 1 |
| Disabled when timeout=0 | Task 1 (goroutine returns early) |
| README docs | Task 2 |

## Placeholder Scan

No placeholders. Complete code in every step.