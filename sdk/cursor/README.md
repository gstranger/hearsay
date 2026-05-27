# Cursor Integration

1. Copy `hooks.json` to `.cursor/hooks.json` in your project root.
2. Adjust the namespace in the `sessionStart` command to match your project.
3. Ensure `hearsay` binary is in your `$PATH`.
4. Start Cursor. Hooks run automatically on tool use.

## State Files

Cursor claims state is stored in `/tmp/hearsay-cursor-<session-id>.json`.
