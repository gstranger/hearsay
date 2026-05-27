# Coordination Etiquette Skill

You are part of a multi-agent fleet coordinated via **agentstate**. Your harness automatically claims resources before editing and releases them when done. You only need to learn three behaviors.

## 1. Query before planning

Before starting a task, check if other agents are already working on the same files:

```bash
agentstate query --resource "file://repo/src/auth/**"
```

Read the **intent strings** of active claims. They tell you what another agent is doing.

## 2. Write intent strings other agents can read

When your harness claims a resource, it generates an intent. You can improve it:

- **Bad:** `"fixing stuff"`
- **Good:** `"refactoring validateToken() to use opaque tokens instead of JWT"`

If your harness supports it, update the intent after you understand the problem:

```bash
agentstate intent <claim-id> "refactoring auth: extracting token validation into its own module"
```

## 3. On conflict, yield and replan

If another agent is already editing a file you planned to touch:

1. **Don't retry** with higher priority. The claims protocol is advisory, not a lock.
2. **Query** the conflicting agent's intent. Decide:
   - Wait for them to finish, then continue.
   - Work on something else and come back.
   - Escalate to human/planner if the conflict is fundamental.
3. **Release your claim** if you abandon the task:
   ```bash
   agentstate release <claim-id> --outcome abandoned
   ```

## 4. Manual claims when coordination is unavailable

If the agentstate server is unreachable (network issue, not configured):

1. Your harness will warn you and proceed uncoordinated.
2. You can still **manually claim** before risky edits:
   ```bash
   agentstate claim file://repo/src/auth/session.ts --operation write --intent "upgrading session management"
   ```
3. Remember to **release** when done.

## 5. Filesystem watcher fallback

If your harness does not support hooks (some IDEs, legacy tools), run the watcher:

```bash
agentstate watch --path ./repo --namespace org/repo/branch
```

The watcher detects file changes and creates **retroactive claims**. These are too late to prevent collisions but help future agents understand what changed recently.

## Mailbox Protocol

The agentstate mailbox allows agents to send asynchronous messages to each other for coordination beyond conflict detection.

### Automatic checks (your harness handles these)

Your harness **automatically checks** for critical messages before every tool execution:
- `yield_request` — another agent needs you to pause
- `escalation` — a conflict needs human resolution

If a critical message is found, your harness **blocks the tool** and shows you the message. You must respond before proceeding.

### Manual checks (you handle these)

**Check your mailbox for routine messages at these times:**
1. Before starting a new plan or task
2. After completing work on a file
3. When you see a conflict warning

**Routine message types:**
- `note` — another agent finished work that affects you (e.g., "UserAuth is now AuthService")
- `ping` — liveness check from another agent
- `yield_ack` — confirmation that another agent yielded to you
- `all_clear` — human resolved an escalation, safe to resume

To check manually:
```
GET /mailbox?namespace=<ns>&agent_id=<you>&unread=true
```

### When you detect a conflict

1. Your harness already checked for critical messages automatically
2. If no critical message, check manually for `note` or `yield_ack`
3. If nothing helpful, consider sending a `yield_request`:
   ```
   POST /message
   {
     "to": "<other-agent-id>",
     "type": "yield_request",
     "content": "I need to refactor auth.ts for token validation. Can you yield?",
     "related_claim_id": "<your-claim-id>"
   }
   ```
4. Wait 60 seconds for `yield_ack`
5. If no response, escalate to human:
   ```
   POST /message
   {
     "to": "broadcast",
     "type": "escalation",
     "content": "Conflict with <agent> on auth.ts — no yield after 60s",
     "related_claim_id": "<your-claim-id>"
   }
   ```

### After completing work

If you claimed resources other agents might need:
1. Send a `note` to broadcast:
   ```
   POST /message
   {
     "to": "broadcast",
     "type": "note",
     "content": "Finished refactoring auth.ts — UserAuth is now AuthService",
     "related_claim_id": "<your-claim-id>"
   }
   ```

### Message etiquette

- Keep messages concise (1-2 sentences)
- Always include `related_claim_id` for yield requests and escalations
- Reply to `yield_request` with `yield_ack` or `escalation`
- Don't spam — one message per conflict is enough
- Mark routine messages as read once processed (critical messages are handled automatically)
