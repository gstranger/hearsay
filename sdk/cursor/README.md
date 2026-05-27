# Cursor Integration

1. Copy `hooks.json` to `.cursor/hooks.json` in your project root.
2. Adjust the namespace in the `sessionStart` command to match your project.
3. Ensure `agentstate` binary is in your `$PATH`.
4. Start Cursor. Hooks run automatically on tool use.

## State Files

Cursor claims state is stored in `/tmp/agentstate-cursor-<session-id>.json`.
