package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

// CursorState is persisted to /tmp/hearsay-cursor-<session-id>.json
type CursorState struct {
	SessionID    string            `json:"session_id"`
	Namespace    string            `json:"namespace"`
	AgentID      string            `json:"agent_id"`
	ActiveClaims map[string]string `json:"active_claims"` // toolCallId -> claimId
}

func cursorStateFilePath(sessionID string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("hearsay-cursor-%s.json", sessionID))
}

func loadCursorState(sessionID string) (*CursorState, error) {
	path := cursorStateFilePath(sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &CursorState{SessionID: sessionID, ActiveClaims: map[string]string{}}, nil
		}
		return nil, err
	}
	var state CursorState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.ActiveClaims == nil {
		state.ActiveClaims = map[string]string{}
	}
	return &state, nil
}

func saveCursorState(state *CursorState) error {
	path := cursorStateFilePath(state.SessionID)
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func cmdCursor(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: hearsay cursor <subcommand>")
		fmt.Fprintln(os.Stderr, "Subcommands: session-start, pre-tool-use, post-tool-use, session-end")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "session-start":
		cmdCursorSessionStart(subArgs)
	case "pre-tool-use":
		cmdCursorPreToolUse(subArgs)
	case "post-tool-use":
		cmdCursorPostToolUse(subArgs)
	case "session-end":
		cmdCursorSessionEnd(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown cursor subcommand: %s\n", sub)
		os.Exit(1)
	}
}

func cmdCursorSessionStart(args []string) {
	var sessionID, namespace, agentID string
	for i := 0; i < len(args); i += 2 {
		if i+1 >= len(args) {
			break
		}
		switch args[i] {
		case "--session-id":
			sessionID = args[i+1]
		case "--namespace":
			namespace = args[i+1]
		case "--agent-id":
			agentID = args[i+1]
		}
	}
	if sessionID == "" {
		fmt.Fprintln(os.Stderr, "Missing --session-id")
		os.Exit(1)
	}

	state := &CursorState{
		SessionID:    sessionID,
		Namespace:    namespace,
		AgentID:      agentID,
		ActiveClaims: map[string]string{},
	}
	if err := saveCursorState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save state: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(`{"permission":"allow"}`)
}

func cmdCursorPreToolUse(args []string) {
	var sessionID, toolName, toolInput, onConflict string
	for i := 0; i < len(args); i += 2 {
		if i+1 >= len(args) {
			break
		}
		switch args[i] {
		case "--session-id":
			sessionID = args[i+1]
		case "--tool":
			toolName = args[i+1]
		case "--input":
			toolInput = args[i+1]
		case "--on-conflict":
			onConflict = args[i+1]
		}
	}
	if sessionID == "" || toolName == "" {
		fmt.Fprintln(os.Stderr, "Missing --session-id or --tool")
		os.Exit(1)
	}

	// Fall back to env var if flag not provided
	if onConflict == "" {
		onConflict = os.Getenv("HEARSAY_ON_CONFLICT")
	}
	if onConflict == "" {
		onConflict = "warn"
	}

	state, err := loadCursorState(sessionID)
	if err != nil {
		fmt.Println(`{"permission":"allow","agent_message":"hearsay coordination unavailable — proceeding uncoordinated"}`)
		os.Exit(0)
	}

	ctx := context.Background()
	client, err := loadClient()
	if err != nil {
		fmt.Println(`{"permission":"allow","agent_message":"hearsay coordination unavailable — proceeding uncoordinated"}`)
		os.Exit(0)
	}

	// Map tool to resource URI and operation
	var resourceURI, operation string
	switch toolName {
	case "Read", "Write", "Edit":
		// Parse input as JSON to extract file_path
		var input map[string]interface{}
		if err := json.Unmarshal([]byte(toolInput), &input); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to parse tool input: %v\n", err)
			fmt.Println(`{"permission":"allow"}`)
			os.Exit(0)
		}
		if path, ok := input["file_path"].(string); ok {
			resourceURI = "file://" + path
		}
		if path, ok := input["path"].(string); ok {
			resourceURI = "file://" + path
		}
		if toolName == "Read" {
			operation = "read"
		} else {
			operation = "write"
		}
	case "Bash":
		operation = "write"
		var input map[string]interface{}
		json.Unmarshal([]byte(toolInput), &input)
		if cmd, ok := input["command"].(string); ok {
			h := sha256.Sum256([]byte(cmd))
			resourceURI = "exec://bash/" + hex.EncodeToString(h[:])[:6]
		}
	default:
		fmt.Println(`{"permission":"allow"}`)
		os.Exit(0)
	}

	if resourceURI == "" {
		fmt.Println(`{"permission":"allow"}`)
		os.Exit(0)
	}

	req := hearsay.ClaimRequest{
		AgentID:     state.AgentID,
		ResourceURI: resourceURI,
		Operation:   hearsay.Operation(operation),
		Intent:      fmt.Sprintf("cursor-%s: %s", toolName, resourceURI),
		TTLSeconds:  300,
	}

	resp, err := client.Claim(ctx, req)
	if err != nil {
		fmt.Println(`{"permission":"allow","agent_message":"hearsay coordination unavailable — proceeding uncoordinated"}`)
		os.Exit(0)
	}

	if resp.Conflict != nil && resp.Conflict.HasConflict {
		c := resp.Conflict.Conflicts[0]
		msg := fmt.Sprintf("Conflict: %s is %s %s (%s)", c.AgentID, c.Operation, c.Resource, c.Intent)
		switch onConflict {
		case "block":
			fmt.Println(`{"permission":"deny"}`)
			os.Exit(2)
		case "allow":
			fmt.Println(`{"permission":"allow"}`)
		default: // "warn" or anything else
			fmt.Printf(`{"permission":"allow","agent_message":"⚠️ %s"}`+"\n", msg)
		}
		return
	}

	// Store claim ID for this tool call (hash toolInput to avoid huge keys)
	h := sha256.Sum256([]byte(toolInput))
	toolCallId := fmt.Sprintf("%s:%s", toolName, hex.EncodeToString(h[:])[:8])
	state.ActiveClaims[toolCallId] = resp.ClaimID
	_ = saveCursorState(state)

	fmt.Println(`{"permission":"allow"}`)
}

func cmdCursorPostToolUse(args []string) {
	var sessionID, toolName, toolInput string
	for i := 0; i < len(args); i += 2 {
		if i+1 >= len(args) {
			break
		}
		switch args[i] {
		case "--session-id":
			sessionID = args[i+1]
		case "--tool":
			toolName = args[i+1]
		case "--input":
			toolInput = args[i+1]
		}
	}
	if sessionID == "" {
		fmt.Fprintln(os.Stderr, "Missing --session-id")
		os.Exit(1)
	}

	state, err := loadCursorState(sessionID)
	if err != nil {
		os.Exit(0)
	}

	ctx := context.Background()
	client, _ := loadClient()

	h := sha256.Sum256([]byte(toolInput))
	toolCallId := fmt.Sprintf("%s:%s", toolName, hex.EncodeToString(h[:])[:8])
	claimId, ok := state.ActiveClaims[toolCallId]
	if !ok {
		os.Exit(0)
	}

	if client != nil {
		_ = client.Release(ctx, claimId, hearsay.OutcomeSucceeded)
	}
	delete(state.ActiveClaims, toolCallId)
	_ = saveCursorState(state)
	fmt.Println(`{"permission":"allow"}`)
}

func cmdCursorSessionEnd(args []string) {
	var sessionID string
	for i := 0; i < len(args); i += 2 {
		if i+1 >= len(args) {
			break
		}
		if args[i] == "--session-id" {
			sessionID = args[i+1]
		}
	}
	if sessionID == "" {
		fmt.Fprintln(os.Stderr, "Missing --session-id")
		os.Exit(1)
	}

	state, err := loadCursorState(sessionID)
	if err != nil {
		os.Exit(0)
	}

	ctx := context.Background()
	client, _ := loadClient()

	for _, claimId := range state.ActiveClaims {
		if client != nil {
			_ = client.Release(ctx, claimId, hearsay.OutcomeAbandoned)
		}
	}

	_ = os.Remove(cursorStateFilePath(sessionID))
	fmt.Println(`{"permission":"allow"}`)
}
