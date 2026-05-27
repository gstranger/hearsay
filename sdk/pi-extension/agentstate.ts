import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { createHash } from "node:crypto";
import { spawn } from "node:child_process";
import { access } from "node:fs/promises";

interface ExtensionConfig {
  endpoint: string;
  namespace: string;
  agentId: string;
  defaultTTL: number;
  claimOnRead: boolean;
  hearsayBinary: string; // path to hearsay binary, or "hearsay" to find in PATH
  onConflict: "block" | "warn" | "allow";
}

function getConfig(): ExtensionConfig {
  return {
    endpoint: process.env.HEARSAY_ENDPOINT || "http://localhost:8080",
    namespace: process.env.HEARSAY_NAMESPACE || "default",
    agentId: process.env.HEARSAY_AGENT_ID || `pi:unknown:${Date.now()}`,
    defaultTTL: parseInt(process.env.HEARSAY_TTL || "300", 10),
    claimOnRead: process.env.HEARSAY_CLAIM_ON_READ === "true",
    hearsayBinary: process.env.HEARSAY_BINARY || "hearsay",
    onConflict: ["block", "warn", "allow"].includes(process.env.HEARSAY_ON_CONFLICT as string)
      ? (process.env.HEARSAY_ON_CONFLICT as ExtensionConfig["onConflict"])
      : "warn",
  };
}

/** Tries to fetch the health endpoint. Returns true if serve is already running. */
async function isServeRunning(endpoint: string): Promise<boolean> {
  try {
    const resp = await fetch(`${endpoint}/health`, { signal: AbortSignal.timeout(500) });
    return resp.ok;
  } catch {
    return false;
  }
}

/** Auto-starts `hearsay serve` in the background if not already running. */
async function ensureServe(cfg: ExtensionConfig, pi: ExtensionAPI): Promise<void> {
  if (await isServeRunning(cfg.endpoint)) return;

  // Check if we can find the binary
  const bin = cfg.hearsayBinary;
  try {
    if (bin.includes("/")) await access(bin); // absolute path check
  } catch {
    console.warn("[hearsay] hearsay binary not found — install with:\n  go install github.com/thunder/hearsay/cmd/hearsay@latest");
    return;
  }

  // Check for .hearsay.toml config
  let initNeeded = false;
  try {
    await access(".hearsay.toml");
  } catch {
    initNeeded = true;
  }

  const args: string[] = [];
  if (initNeeded) {
    args.push("init", "--provider", "sqlite", "--namespace", cfg.namespace);
    // Run init first
    try {
      await new Promise<void>((resolve, reject) => {
        const child = spawn(bin, args, { stdio: "ignore", detached: true });
        child.on("close", (code) => (code === 0 ? resolve() : reject(new Error(`init exited ${code}`))));
      });
    } catch {
      console.warn("[hearsay] failed to auto-init config");
    }
  }

  // Start serve in background
  const port = cfg.endpoint.match(/:(\d+)$/)?.[1] || "8080";
  const serveProcess = spawn(bin, ["serve", "--addr", `localhost:${port}`], {
    stdio: "ignore",
    detached: true,
  });
  serveProcess.unref();

  // Wait briefly for server to come up
  for (let i = 0; i < 20; i++) {
    if (await isServeRunning(cfg.endpoint)) {
      console.log("[hearsay] auto-started hearsay serve");
      return;
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  console.warn("[hearsay] serve did not start in time");
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
    return { ok: true, reason: `hearsay unavailable (${err})` };
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
    case "edit":
    case "write":
    case "bash": return "write";
    default: return null;
  }
}

function inputToURI(toolName: string, input: any): string | null {
  switch (toolName) {
    case "read":
    case "write":
    case "edit": {
      const path = input?.path ?? input?.file_path;
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

export default async function (pi: ExtensionAPI) {
  const cfg = getConfig();
  await ensureServe(cfg, pi);
  const activeClaims = new Map<string, string>(); // toolCallId -> claimId

  pi.on("tool_call", async (event, _ctx) => {
    if (!["read", "write", "edit", "bash"].includes(event.toolName)) return;

    // Check for critical mailbox messages first
    const criticalMsgs = await checkCriticalMailbox(cfg.endpoint, cfg.namespace, cfg.agentId);
    if (criticalMsgs.length > 0) {
      return {
        block: true,
        reason: `📬 Critical mailbox messages — you must respond before proceeding:\n${criticalMsgs.join("\n")}`,
      };
    }

    const uri = inputToURI(event.toolName, event.input);
    if (!uri) return;

    const op = toolToOp(event.toolName);
    if (!op) return;
    if (op === "read" && !cfg.claimOnRead) return;

    const intent = `pi-${event.toolName}: ${uri}`;
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
          return { block: true, reason: result.reason || "Blocked by hearsay" };
        case "allow":
          // Silently allow without tracking a claim (none was granted during conflict)
          return { block: false };
        case "warn":
        default:
          // Allow but show warning message
          return { block: false, message: `⚠️ ${result.reason}` };
      }
    }

    if (result.claimId) {
      activeClaims.set(event.toolCallId, result.claimId);
    }
  });

  pi.on("tool_result", async (event, _ctx) => {
    const claimId = activeClaims.get(event.toolCallId);
    if (!claimId) return;

    const outcome = event.isError ? "failed" : "succeeded";
    await releaseClaim(cfg.endpoint, cfg.namespace, claimId, outcome);
    activeClaims.delete(event.toolCallId);
  });

  pi.on("session_shutdown", async () => {
    for (const [_, claimId] of activeClaims) {
      await releaseClaim(cfg.endpoint, cfg.namespace, claimId, "abandoned");
    }
    activeClaims.clear();
  });
}
