//go:build !wasm

package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: hearsay <command> [args]")
		fmt.Fprintln(os.Stderr, "Commands: init, claim, release, heartbeat, query, check, namespace, serve, cursor, watch, status")
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "init":
		cmdInit(os.Args[2:])
	case "claim":
		cmdClaim(os.Args[2:])
	case "release":
		cmdRelease(os.Args[2:])
	case "heartbeat":
		cmdHeartbeat(os.Args[2:])
	case "query":
		cmdQuery(os.Args[2:])
	case "check":
		cmdCheck(os.Args[2:])
	case "namespace":
		cmdNamespace(os.Args[2:])
	case "serve":
		cmdServe(os.Args[2:])
	case "watch":
		cmdWatch(os.Args[2:])
	case "cursor":
		cmdCursor(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}