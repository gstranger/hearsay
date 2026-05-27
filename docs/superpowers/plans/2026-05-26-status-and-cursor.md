# Status Subcommand & Cursor State Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `hearsay status` command for runtime visibility and move Cursor state from `/tmp` to `~/.config/hearsay/cursor/`.

**Architecture:** New `cmdStatus` function in `cmd/hearsay/main.go` queries the provider directly. Cursor state path updated to use `os.UserConfigDir()` with fallback.

**Tech Stack:** Go 1.25 standard library

---

### File Mapping

| File | Responsibility |
|---|---|
| `cmd/hearsay/main.go` | Add `status` case to command switch, new `cmdStatus` function |
| `cmd/hearsay/cursor.go` | Update state path from `/tmp` to config dir |
| `cmd/hearsay/cursor_test.go` | Update test paths |
| `README.md` | Document `hearsay status` |

---

### Task 1: Move Cursor State to Config Directory

**Files:**
- Modify: `cmd/hearsay/cursor.go`
- Modify: `cmd/hearsay/cursor_test.go`

- [ ] **Step 1: Read current cursor.go state path**

```bash
cd ~/Documents/agentstate
grep -n "tmp\|state\|cursor" cmd/hearsay/cursor.go
```

- [ ] **Step 2: Update cursor state path**

Replace the hardcoded `/tmp/hearsay-cursor-state.json` with a platform-appropriate path. In `cmd/hearsay/cursor.go`, find the state path variable and change it to:

```go
import "os"

func cursorStateDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir() // fallback
	}
	dir := filepath.Join(configDir, "hearsay", "cursor")
	os.MkdirAll(dir, 0755)
	return dir
}

func cursorStatePath() string {
	return filepath.Join(cursorStateDir(), "state.json")
}
```

Update all references from the old hardcoded path to `cursorStatePath()`.

- [ ] **Step 3: Update cursor_test.go**

Replace any reference to `/tmp/hearsay-cursor-state.json` in `cmd/hearsay/cursor_test.go` with the new path. Add cleanup:

```go
func TestCursorStatePath(t *testing.T) {
	path := cursorStatePath()
	if !strings.Contains(path, "hearsay") || !strings.Contains(path, "cursor") {
		t.Fatalf("unexpected cursor state path: %s", path)
	}
}
```

- [ ] **Step 4: Ensure imports are correct**

Add needed imports:
```go
import (
	"os"
	"path/filepath"
	// ... existing imports
)
```

- [ ] **Step 5: Run tests**

```bash
cd ~/Documents/agentstate
go test ./cmd/hearsay/... -run TestCursor -v
```

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add cmd/hearsay/cursor.go cmd/hearsay/cursor_test.go
git commit -m "feat: move cursor state from /tmp to user config directory"
```

---

### Task 2: Add `hearsay status` Subcommand

**Files:**
- Modify: `cmd/hearsay/main.go`

- [ ] **Step 1: Add `status` to command switch**

In `main()`, add the `status` case:

```go
case "status":
    cmdStatus(os.Args[2:])
```

- [ ] **Step 2: Implement `cmdStatus` function**

```go
func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	verbose := fs.Bool("verbose", false, "Show detailed claim and mailbox activity")
	fs.Parse(args)

	provider, err := loadProvider()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cfg, err := hearsay.LoadConfig(".hearsay.toml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Active claims
	claims, err := provider.ActiveClaims(ctx, cfg.Namespace, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error querying claims: %v\n", err)
		os.Exit(1)
	}

	// Mailbox messages
	msgs, err := provider.Query(ctx, cfg.Namespace, hearsay.QueryOpts{Limit: 100})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error querying mailbox: %v\n", err)
		os.Exit(1)
	}
	unread := 0
	for _, m := range msgs {
		if !m.Read {
			unread++
		}
	}

	// Agents online (unique agent IDs from active claims)
	agents := make(map[string]bool)
	for _, c := range claims {
		agents[c.AgentID] = true
	}

	// Provider info
	providerInfo := cfg.Provider
	if cfg.Provider == "sqlite" {
		path := ".hearsay.db"
		if cfg.ProviderCfg.SQLite != nil && cfg.ProviderCfg.SQLite.Path != "" {
			path = cfg.ProviderCfg.SQLite.Path
		}
		providerInfo = fmt.Sprintf("sqlite (%s)", path)
	}

	fmt.Printf("Namespace:       %s\n", cfg.Namespace)
	fmt.Printf("Provider:        %s\n", providerInfo)
	fmt.Printf("Active claims:   %d\n", len(claims))
	fmt.Printf("Mailbox:         %d messages (%d unread)\n", len(msgs), unread)

	agentList := make([]string, 0, len(agents))
	for a := range agents {
		agentList = append(agentList, a)
	}
	fmt.Printf("Agents online:   %d", len(agents))
	if len(agentList) > 0 {
		fmt.Printf(" (%s)", strings.Join(agentList, ", "))
	}
	fmt.Println()

	if *verbose {
		fmt.Println()
		if len(claims) > 0 {
			fmt.Println("Active claims:")
			for _, c := range claims {
				age := time.Since(c.CreatedAt).Truncate(time.Second)
				fmt.Printf("  %-10s %-30s %-8s %-15s %s ago\n",
					c.AgentID, c.ResourceURI, c.Operation, c.Intent, age)
			}
			fmt.Println()
		}

		if len(msgs) > 0 {
			fmt.Println("Recent mailbox (last 10):")
			for i, m := range msgs {
				if i >= 10 {
					break
				}
				status := " "
				if m.Read {
					status = "✓"
				}
				age := time.Since(m.CreatedAt).Truncate(time.Second)
				fmt.Printf("  %s %-10s → %-10s %-20s %s ago\n",
					status, m.From, m.To, m.Type, age)
			}
		}
	}
}
```

- [ ] **Step 3: Add required imports**

Ensure these imports exist in `cmd/hearsay/main.go`:
```go
import (
	"context"
	"strings"
	// ... existing imports
)
```

- [ ] **Step 4: Build and test**

```bash
cd ~/Documents/agentstate
go build ./cmd/hearsay
go test ./...
```

Expected: builds successfully, all 11 packages pass

- [ ] **Step 5: Manual test**

```bash
# Initialize and claim something
./hearsay init
./hearsay claim file://test.go --agent-id agent-a --operation write --intent "testing"

# Check status
./hearsay status
# Expected: shows namespace, provider, active claims, mailbox, agents online

# Verbose
./hearsay status --verbose
# Expected: shows claim details

# Cleanup
./hearsay release <claim-id> --outcome abandoned
```

- [ ] **Step 6: Commit**

```bash
git add cmd/hearsay/main.go
git commit -m "feat: add hearsay status subcommand with verbose mode"
```

---

### Task 3: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add `status` to command list**

Find the commands section and add:
```bash
hearsay status          # Show runtime state
hearsay status --verbose # Show detailed claims and mailbox
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: document hearsay status subcommand"
```

---

### Task 4: Final Verification

- [ ] **Step 1: Run full test suite**

```bash
cd ~/Documents/agentstate
go test ./...
```

Expected: all packages pass

- [ ] **Step 2: Build binary**

```bash
go build ./cmd/hearsay
```

Expected: creates `./hearsay` binary

- [ ] **Step 3: Test help output**

```bash
./hearsay --help 2>&1 | grep status
```

Expected: shows `status` in command list

- [ ] **Step 4: Test Cursor state path**

```bash
go test ./cmd/hearsay/... -run TestCursor -v
```

Expected: PASS, shows config dir path

---

## Spec Coverage Check

| Spec Requirement | Task |
|---|---|
| `hearsay status` command | Task 2 |
| `--verbose` flag | Task 2 |
| Namespace, provider, claims count display | Task 2 |
| Mailbox count (total/unread) | Task 2 |
| Agents online | Task 2 |
| Cursor state to `os.UserConfigDir()` | Task 1 |
| README docs | Task 3 |
| Final verification | Task 4 |

## Placeholder Scan

No placeholders. All steps contain actual code.

## Type Consistency Check

- `cursorStatePath()` returns string — used in cursor.go and cursor_test.go
- `hearsay.QueryOpts` — already exists in `pkg/hearsay/provider.go`
- `provider.ActiveClaims` — already exists in provider interface
- `provider.Query` — already exists, used for mailbox query