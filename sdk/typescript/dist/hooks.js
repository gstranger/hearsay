import { AgentstateClient } from "./client.js";
import { toolToURI } from "./uri.js";
function stableCallId(toolName, input) {
    const sorted = Object.keys(input)
        .sort()
        .reduce((acc, k) => {
        acc[k] = input[k];
        return acc;
    }, {});
    return `${toolName}:${JSON.stringify(sorted)}`;
}
function resolveConflict(mode, conflict) {
    const message = `Conflict: ${conflict.agent_id} is ${conflict.operation} ${conflict.resource} (${conflict.intent})`;
    switch (mode) {
        case "block":
            return { permissionDecision: "deny", agentMessage: message };
        case "allow":
            return { permissionDecision: "allow" };
        case "warn":
            return { permissionDecision: "allow", agentMessage: `⚠️ ${message}` };
        default:
            console.warn(`[agentstate] invalid onConflict mode "${mode}", falling back to "warn"`);
            return { permissionDecision: "allow", agentMessage: `⚠️ ${message}` };
    }
}
function formatMailboxMessage(msg) {
    return `${msg.from}: ${msg.content}`;
}
async function checkCriticalMailbox(client) {
    try {
        const msgs = await client.getMailbox({ unread: true });
        return msgs.filter((m) => m.type === "yield_request" || m.type === "escalation");
    }
    catch {
        return [];
    }
}
export function createHooks(opts) {
    const activeClaims = new Map(); // callId -> claimId
    const heartbeats = new Map(); // claimId -> timer
    const startHeartbeat = (claimId, ttlSeconds) => {
        // Default is true; skip only if explicitly false
        if (opts.autoHeartbeat === false)
            return;
        const intervalMs = (ttlSeconds * 1000) / 2;
        // Defensive: clear any existing timer for this claimId
        const existing = heartbeats.get(claimId);
        if (existing)
            clearInterval(existing);
        let warned = false;
        const timer = setInterval(async () => {
            try {
                await hooks.client.heartbeat(claimId);
                warned = false; // reset on success
            }
            catch (err) {
                if (!warned) {
                    console.warn(`[agentstate] heartbeat failed for ${claimId}: ${err}`);
                    warned = true;
                }
            }
        }, intervalMs);
        heartbeats.set(claimId, timer);
    };
    const stopHeartbeat = (claimId) => {
        const timer = heartbeats.get(claimId);
        if (timer) {
            clearInterval(timer);
            heartbeats.delete(claimId);
        }
    };
    const toolToOp = (toolName) => {
        switch (toolName) {
            case "Read": return "read";
            case "Write": return "write";
            case "Edit": return "write";
            case "Bash": return "write";
            case "Glob": return "read";
            default: return null;
        }
    };
    const hooks = {
        client: new AgentstateClient(opts),
        async preToolUse(toolName, input) {
            // Check for critical mailbox messages first
            const criticalMsgs = await checkCriticalMailbox(hooks.client);
            if (criticalMsgs.length > 0) {
                const messages = criticalMsgs.map(formatMailboxMessage).join("\n");
                return {
                    permissionDecision: "deny",
                    agentMessage: `📬 Critical mailbox messages — you must respond before proceeding:\n${messages}`,
                };
            }
            const uri = toolToURI(toolName, input);
            if (!uri)
                return { permissionDecision: "allow" };
            const op = toolToOp(toolName);
            if (!op)
                return { permissionDecision: "allow" };
            if (op === "read" && !opts.claimOnRead)
                return { permissionDecision: "allow" };
            const intent = `auto-claim: ${toolName} on ${uri}`;
            const callId = stableCallId(toolName, input);
            try {
                const resp = await hooks.client.claim(uri, op, intent);
                // Server locking mode: returns 423 with error="resource_locked"
                if (resp.error === "resource_locked") {
                    const lockedBy = resp.locked_by ?? "another agent";
                    const lockedIntent = resp.intent ?? "unknown intent";
                    return {
                        permissionDecision: "deny",
                        agentMessage: `🔒 Resource locked by ${lockedBy} (${lockedIntent}). You must yield or escalate.`,
                    };
                }
                if (resp.conflicts?.has_conflict) {
                    const c = resp.conflicts.conflicts?.[0];
                    if (!c) {
                        return { permissionDecision: "allow", agentMessage: "⚠️ Conflict detected but details unavailable" };
                    }
                    const mode = opts.onConflict ?? "warn";
                    return resolveConflict(mode, c);
                }
                activeClaims.set(callId, resp.claim_id);
                startHeartbeat(resp.claim_id, opts.defaultTTL ?? 300);
                return { permissionDecision: "allow" };
            }
            catch (err) {
                return {
                    permissionDecision: "allow",
                    agentMessage: `agentstate unavailable — proceeding uncoordinated (${err})`,
                };
            }
        },
        async postToolUse(toolName, input, _result) {
            const callId = stableCallId(toolName, input);
            const claimId = activeClaims.get(callId);
            if (!claimId)
                return;
            stopHeartbeat(claimId); // stop BEFORE releasing
            try {
                await hooks.client.release(claimId, "succeeded");
            }
            catch {
                // ignore release errors
            }
            activeClaims.delete(callId);
        },
        async sessionStart() {
            // no-op for now — SDK hooks are called per-tool-use
        },
        async sessionEnd() {
            // Stop all heartbeats before releasing to avoid stale heartbeats firing
            for (const timer of heartbeats.values()) {
                clearInterval(timer);
            }
            heartbeats.clear();
            for (const [callId, claimId] of activeClaims) {
                try {
                    await hooks.client.release(claimId, "abandoned");
                }
                catch {
                    // ignore
                }
            }
            activeClaims.clear();
        },
        // Diagnostics for testing
        _activeClaimsCount: () => activeClaims.size,
        _heartbeatCount: () => heartbeats.size,
    };
    return hooks;
}
