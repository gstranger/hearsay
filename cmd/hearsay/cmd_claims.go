package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

func cmdClaim(args []string) {
	fs := flag.NewFlagSet("claim", flag.ExitOnError)
	resource := fs.String("resource", "", "resource URI")
	op := fs.String("operation", "write", "operation")
	intent := fs.String("intent", "", "intent string")
	agentID := fs.String("agent", os.Getenv("HEARSAY_AGENT_ID"), "agent ID")
	fs.Parse(args)

	if *resource == "" {
		fmt.Fprintln(os.Stderr, "Usage: hearsay claim --resource <uri> --operation <op> --intent <intent>")
		os.Exit(1)
	}

	client, err := loadClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	resp, err := client.Claim(context.Background(), hearsay.ClaimRequest{
		ResourceURI: *resource,
		AgentID:     *agentID,
		Operation:   hearsay.Operation(*op),
		Intent:      *intent,
	})
	if err != nil {
		if ce, ok := err.(*hearsay.ConflictError); ok {
			data, _ := json.MarshalIndent(ce.Report, "", "  ")
			fmt.Fprintf(os.Stderr, "Conflict:\n%s\n", data)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Claimed: %s\n", resp.ClaimID)
}

func cmdRelease(args []string) {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	claimID := fs.String("claim", "", "claim ID")
	outcome := fs.String("outcome", "succeeded", "outcome")
	fs.Parse(args)

	if *claimID == "" {
		fmt.Fprintln(os.Stderr, "Usage: hearsay release --claim <id>")
		os.Exit(1)
	}

	client, err := loadClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := client.Release(context.Background(), *claimID, hearsay.Outcome(*outcome)); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Released")
}

func cmdHeartbeat(args []string) {
	fs := flag.NewFlagSet("heartbeat", flag.ExitOnError)
	claimID := fs.String("claim", "", "claim ID")
	fs.Parse(args)

	if *claimID == "" {
		fmt.Fprintln(os.Stderr, "Usage: hearsay heartbeat --claim <id>")
		os.Exit(1)
	}

	client, err := loadClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := client.Heartbeat(context.Background(), *claimID); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Heartbeat sent")
}

func cmdQuery(args []string) {
	fs := flag.NewFlagSet("query", flag.ExitOnError)
	resource := fs.String("resource", "", "resource pattern")
	fs.Parse(args)

	client, err := loadClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	claims, err := client.ActiveClaims(context.Background(), *resource)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(claims, "", "  ")
	fmt.Println(string(data))
}

func cmdCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	resource := fs.String("resource", "", "resource URI")
	op := fs.String("operation", "write", "operation")
	fs.Parse(args)

	if *resource == "" {
		fmt.Fprintln(os.Stderr, "Usage: hearsay check --resource <uri> --operation <op>")
		os.Exit(1)
	}

	client, err := loadClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	report, err := client.CheckConflict(context.Background(), *resource, hearsay.Operation(*op))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(data))
}