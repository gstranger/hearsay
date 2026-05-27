# OpenCode Extension Verification Guide

## Unit Tests (Automated)

```bash
cd hearsay/sdk/opencode-extension
npm install
npm test
```

**20 tests** covering:
- Configuration parsing (env vars, defaults, invalid values)
- Auto-start behavior (serve running / not running)
- `tool.execute.before`: claim creation for write/read/bash
- `tool.execute.before`: conflict handling (block/allow/warn)
- `tool.execute.before`: graceful degradation when unreachable
- `tool.execute.after`: claim release
- `tool.execute.after`: warning prepending (experimental)
- `session.status`: bulk claim release
- URI generation (file://, exec://)

## Manual Integration Test

Since we can't run OpenCode in CI, here's how to verify the plugin works end-to-end:

### Prerequisites

1. Install OpenCode: https://opencode.ai
2. Install hearsay: `go install github.com/gstranger/hearsay/cmd/hearsay@latest`
3. Have a project with `.opencode/plugins/` directory

### Setup

```bash
# 1. Copy plugin to your project
cp hearsay/sdk/opencode-extension/hearsay.ts your-project/.opencode/plugins/

# 2. Set env vars
export HEARSAY_NAMESPACE=test/manual
export HEARSAY_AGENT_ID=opencode:manual-test
export HEARSAY_ON_CONFLICT=block  # Start with block mode

# 3. Start OpenCode in your project directory
opencode
```

### Test Cases

#### Test 1: Auto-start

1. Ensure no hearsay server is running: `pkill hearsay`
2. Start OpenCode
3. Ask the agent: "Read the file README.md"
4. **Expected**: The plugin auto-starts `hearsay serve` in the background
5. Verify: `curl http://localhost:8080/health` should return 200

#### Test 2: Block mode (default)

1. In OpenCode, ask: "Write 'hello' to test.txt"
2. The agent should execute the write tool
3. In a second terminal, simulate another agent:
   ```bash
   curl -X POST http://localhost:8080/claim \
     -H "Content-Type: application/json" \
     -d '{"namespace":"test/manual","agent_id":"other-agent","resource_uri":"file://test.txt","operation":"write","intent":"other test","ttl_seconds":300}'
   ```
4. Back in OpenCode, ask: "Write 'world' to test.txt"
5. **Expected**: The tool fails with error: `Conflict: other-agent is write file://test.txt`
6. The agent should see this error and be unable to write

#### Test 3: Allow mode

1. Stop OpenCode, set: `export HEARSAY_ON_CONFLICT=allow`
2. Repeat the claim from Test 2 (or wait for it to expire and re-create)
3. Ask: "Write 'world' to test.txt"
4. **Expected**: Tool proceeds silently, file is written
5. Verify: `cat test.txt` should show "world"

#### Test 4: Warn mode (experimental)

1. Stop OpenCode, set: `export HEARSAY_ON_CONFLICT=warn`
2. Repeat the claim from Test 2
3. Ask: "Write 'warned' to test.txt"
4. **Expected**: Tool proceeds, file is written
5. **Check**: Does the agent see a warning message about the conflict?
   - If YES: The `output.result` mutation works! Document this.
   - If NO: The warn mode is log-only. This is the expected limitation.

#### Test 5: Session cleanup

1. Create a claim by asking the agent to write a file
2. End the OpenCode session (type `exit` or close)
3. Query claims:
   ```bash
   curl "http://localhost:8080/query?namespace=test/manual"
   ```
4. **Expected**: No active claims from your agent (released as "abandoned")

#### Test 6: Graceful degradation

1. Start OpenCode with a working server
2. Kill the server mid-session: `pkill hearsay`
3. Ask the agent to write another file
4. **Expected**: Tool proceeds with a console warning, no crash

### What to Report

If you run these tests, please open an issue with:
- OpenCode version
- Which tests passed/failed
- For Test 4 (warn mode): Does the agent see the warning?
- Any console errors or unexpected behavior

### Known Limitations

1. **No auto-heartbeat**: OpenCode hooks are per-call; long-running operations may see claims expire
2. **Warn mode is experimental**: May not reach the agent depending on OpenCode's internal handling
3. **Block mode is recommended**: The agent is guaranteed to see the conflict message
