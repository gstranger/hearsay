import { createHash } from "node:crypto";
import { spawn } from "node:child_process";
import { access } from "node:fs/promises";

interface ExtensionConfig {
  endpoint: string;
  namespace: string;
  agentId: string;
  defaultTTL: number;
  claimOnRead: boolean;
  agentstateBinary: string;
  onConflict: "block" | "warn" | "allow";
}

function getConfig(): ExtensionConfig {
  return {
    endpoint: process.env.AGENTSTATE_ENDPOINT || "http://localhost:8080",
    namespace: process.env.AGENTSTATE_NAMESPACE || "default",
    agentId: process.env.AGENTSTATE_AGENT_ID || `opencode:${Date.now()}`,
    defaultTTL: parseInt(process.env.AGENTSTATE_TTL || "300", 10),
    claimOnRead: process.env.AGENTSTATE_CLAIM_ON_READ === "true",
    agentstateBinary: process.env.AGENTSTATE_BINARY || "agentstate",
    onConflict: ["block", "warn", "allow"].includes(process.env.AGENTSTATE_ON_CONFLICT as string)
      ? (process.env.AGENTSTATE_ON_CONFLICT as ExtensionConfig["onConflict"])
      : "block",
  };
}

async function isServeRunning(endpoint: string): Promise<boolean> {
  try {
    const resp = await fetch(`${endpoint}/health`, { signal: AbortSignal.timeout(500) });
    return resp.ok;
  } catch {
    return false;
  }
}

async function ensureServe(cfg: ExtensionConfig): Promise<void> {
  if (await isServeRunning(cfg.endpoint)) return;

  const bin = cfg.agentstateBinary;
  try {
    if (bin.includes("/")) await access(bin);
  } catch {
    console.warn("[agentstate] agentstate binary not found — install with:\n  go install github.com/thunder/agentstate/cmd/agentstate@latest");
    return;
  }

  let initNeeded = false;
  try {
    await access(".agentstate.toml");
  } catch {
    initNeeded = true;
  }

  if (initNeeded) {
    try {
      await new Promise<void>((resolve, reject) => {
        const child = spawn(bin, ["init", "--provider", "sqlite", "--namespace", cfg.namespace], { stdio: "ignore", detached: true });
        child.on("close", (code) => (code === 0 ? resolve() : reject(new Error(`init exited ${code}`))));
      });
    } catch {
      console.warn("[agentstate] failed to auto-init config");
    }
  }

  const port = cfg.endpoint.match(/:(\d+)$/)?.[1] || "8080";
  const serveProcess = spawn(bin, ["serve", "--addr", `localhost:${port}`], {
    stdio: "ignore",
    detached: true,
  });
  serveProcess.unref();

  for (let i = 0; i < 20; i++) {
    if (await isServeRunning(cfg.endpoint)) {
      console.log("[agentstate] auto-started agentstate serve");
      return;
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  console.warn("[agentstate] serve did not start in time");
}

async function claimResource(
  endpoint: string,
  namespace: string,
  agentId: string,
  resourceURI: string,
  operation: string,
  intent: string,
  ttl: number
): Promise<{ ok: boolean; claimId?: string; reason?: string }> {
  try {
    const resp = await fetch(`${endpoint}/claim`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        namespace,
        agent_id: agentId,
        resource_uri: resourceURI,
        operation,
        intent,
        ttl_seconds: ttl,
      }),
    });
    const body = (await resp.json()) as any;
    if (body.conflicts?.has_conflict) {
      const c = body.conflicts.conflicts[0];
      return {
        ok: false,
        reason: `Conflict: ${c.agent_id} is ${c.operation} ${c.resource} (${c.intent})`,
      };
    }
    return { ok: true, claimId: body.claim_id };
  } catch (err) {
    return { ok: true, reason: `agentstate unavailable (${err})` };
  }
}

async function releaseClaim(
  endpoint: string,
  namespace: string,
  claimId: string,
  outcome: string
): Promise<void> {
  try {
    await fetch(`${endpoint}/release`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ namespace, claim_id: claimId, outcome }),
    });
  } catch {
    // ignore
  }
}

async function checkCriticalMailbox(endpoint: string, namespace: string, agentId: string): Promise<string[]> {
  try {
    const resp = await fetch(`${endpoint}/mailbox?namespace=${encodeURIComponent(namespace)}&agent_id=${encodeURIComponent(agentId)}&unread=true`);
    if (!resp.ok) return [];
    const msgs = await resp.json() as any[];
    return msgs
      .filter((m) => m.type === "yield_request" || m.type === "escalation")
      .map((m) => `${m.from}: ${m.content}`);
  } catch {
    return [];
  }
}

function toolToOp(toolName: string): string | null {
  switch (toolName) {
    case "read": return "read";
    case "write": return "write";
    case "edit": return "write";
    case "bash": return "write";
    default: return null;
  }
}

function inputToURI(toolName: string, input: any): string | null {
  switch (toolName) {
    case "read":
    case "write":
    case "edit": {
      const path = input?.filePath ?? input?.file_path ?? input?.path;
      if (typeof path === "string") return `file://${path}`;
      return null;
    }
    case "bash": {
      const command = input?.command;
      if (typeof command !== "string") return null;
      const hash = createHash("sha256").update(command).digest("hex").slice(0, 6);
      return `exec://bash/${hash}`;
    }
    default:
      return null;
  }
}

export default async function () {
  const cfg = getConfig();
  await ensureServe(cfg);
  const activeClaims = new Map<string, string>(); // toolCallId -> claimId
  const pendingWarnings = new Map<string, string>(); // toolCallId -> warning message

  return {
    "tool.execute.before": async (input: any, _output: any) => {
      const toolName = input.tool;
      if (!["read", "write", "edit", "bash"].includes(toolName)) return;

      // Check for critical mailbox messages first
      const criticalMsgs = await checkCriticalMailbox(cfg.endpoint, cfg.namespace, cfg.agentId);
      if (criticalMsgs.length > 0) {
        throw new Error(`📬 Critical mailbox messages — you must respond before proceeding:\n${criticalMsgs.join("\n")}`);
      }

      const uri = inputToURI(toolName, input.args);
      if (!uri) return;

      const op = toolToOp(toolName);
      if (!op) return;
      if (op === "read" && !cfg.claimOnRead) return;

      const intent = `opencode-${toolName}: ${uri}`;
      const result = await claimResource(
        cfg.endpoint,
        cfg.namespace,
        cfg.agentId,
        uri,
        op,
        intent,
        cfg.defaultTTL
      );

      if (!result.ok) {
        switch (cfg.onConflict) {
          case "block":
            throw new Error(result.reason || "Blocked by agentstate");
          case "allow":
            return; // Silently proceed
          case "warn":
          default:
            // Store warning for after hook to prepend
            pendingWarnings.set(input.toolCallId, `⚠️ ${result.reason}`);
            return;
        }
      }

      if (result.claimId) {
        activeClaims.set(input.toolCallId, result.claimId);
      }
    },

    "tool.execute.after": async (input: any, output: any) => {
      const toolCallId = input.toolCallId;

      // If we have a pending warning, prepend it to the result
      const warning = pendingWarnings.get(toolCallId);
      if (warning) {
        pendingWarnings.delete(toolCallId);
        // Try to prepend to the output — this is the experimental part
        if (output && typeof output.result === "string") {
          output.result = `${warning}\n\n---\n${output.result}`;
        } else if (output && typeof output === "object") {
          output.result = warning;
        }
      }

      // Release claim if we have one
      const claimId = activeClaims.get(toolCallId);
      if (!claimId) return;

      await releaseClaim(cfg.endpoint, cfg.namespace, claimId, "succeeded");
      activeClaims.delete(toolCallId);
    },

    "session.status": async (_input: any, output: any) => {
      // Release all active claims on session end/idle
      for (const [toolCallId, claimId] of activeClaims) {
        await releaseClaim(cfg.endpoint, cfg.namespace, claimId, "abandoned");
      }
      activeClaims.clear();
      pendingWarnings.clear();
    },
  };
}
