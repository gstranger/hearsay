//go:build !wasm

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gstranger/hearsay/internal/watcher"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

func cmdWatch(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	path := fs.String("path", "", "directory path to watch")
	namespace := fs.String("namespace", "default", "namespace ID")
	claimTTL := fs.Int("claim-ttl", 60, "retroactive claim TTL in seconds")
	fs.Parse(args)

	if *path == "" {
		fmt.Fprintln(os.Stderr, "Usage: hearsay watch --path <dir> --namespace <id> [--claim-ttl <seconds>]")
		os.Exit(1)
	}

	client, err := loadClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading client: %v\n", err)
		os.Exit(1)
	}

	w := watcher.New(watcher.Config{
		Path:      *path,
		Namespace: *namespace,
		Client:    client,
		ClaimTTL:  *claimTTL,
	})

	fmt.Printf("Watching %s (namespace: %s, claim TTL: %ds)\n", *path, *namespace, *claimTTL)
	if err := w.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "Watcher error: %v\n", err)
		os.Exit(1)
	}
}

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

	claims, err := provider.ActiveClaims(ctx, cfg.Namespace, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error querying claims: %v\n", err)
		os.Exit(1)
	}

	agents := make(map[string]bool)
	for _, c := range claims {
		agents[c.AgentID] = true
	}

	var allMailboxMsgs []hearsay.MailboxMessage
	totalMailbox := 0
	unreadMailbox := 0
	for agentID := range agents {
		msgs, err := provider.GetMailbox(ctx, cfg.Namespace, agentID, hearsay.MailboxQueryOpts{Limit: 50})
		if err != nil {
			continue
		}
		totalMailbox += len(msgs)
		for _, m := range msgs {
			if !m.Read {
				unreadMailbox++
			}
		}
		allMailboxMsgs = append(allMailboxMsgs, msgs...)
	}

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
	fmt.Printf("Mailbox:         %d messages (%d unread)\n", totalMailbox, unreadMailbox)

	agentList := make([]string, 0, len(agents))
	for a := range agents {
		agentList = append(agentList, a)
	}
	sort.Strings(agentList)
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

		if len(allMailboxMsgs) > 0 {
			fmt.Println("Recent mailbox (last 10):")
			sort.Slice(allMailboxMsgs, func(i, j int) bool {
				return allMailboxMsgs[i].CreatedAt.After(allMailboxMsgs[j].CreatedAt)
			})
			limit := 10
			if len(allMailboxMsgs) < limit {
				limit = len(allMailboxMsgs)
			}
			for _, m := range allMailboxMsgs[:limit] {
				status := " "
				if m.Read {
					status = "✓"
				}
				age := time.Since(m.CreatedAt).Truncate(time.Second)
				fmt.Printf("  %s %-10s → %-10s %-20s %s ago\n",
					status, m.From, m.To, string(m.Type), age)
			}
		}
	}
}