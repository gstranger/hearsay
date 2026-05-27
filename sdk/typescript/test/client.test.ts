import { describe, it, expect, beforeAll, afterAll } from "vitest";
import { AgentstateClient } from "../src/client.js";
import { createServer } from "node:http";

function mockServer(handler: (req: any, res: any) => void) {
  const server = createServer(handler);
  return new Promise<{ server: any; port: number }>((resolve) => {
    server.listen(0, "127.0.0.1", () => {
      resolve({ server, port: (server.address() as any).port });
    });
  });
}

describe("AgentstateClient", () => {
  it("claims a resource", async () => {
    const { server, port } = await mockServer((req, res) => {
      let body = "";
      req.on("data", (c: any) => (body += c));
      req.on("end", () => {
        const parsed = JSON.parse(body);
        expect(parsed.resource_uri).toBe("file://src/auth.ts");
        expect(parsed.operation).toBe("write");
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ claim_id: "claim_123" }));
      });
    });

    const client = new AgentstateClient({
      endpoint: `http://127.0.0.1:${port}`,
      namespace: "test",
      agentId: "claude:test:sess_1",
    });

    const resp = await client.claim("file://src/auth.ts", "write", "refactoring auth");
    expect(resp.claim_id).toBe("claim_123");
    server.close();
  });

  it("returns conflict report on 409", async () => {
    const { server, port } = await mockServer((req, res) => {
      res.writeHead(409, { "Content-Type": "application/json" });
      res.end(JSON.stringify({
        claim_id: "claim_456",
        conflicts: {
          has_conflict: true,
          conflicts: [{
            claim_id: "claim_789",
            agent_id: "agent-2",
            resource: "file://src/auth.ts",
            operation: "write",
            intent: "fixing bug",
            since: "2026-05-21T00:00:00Z",
          }],
        },
      }));
    });

    const client = new AgentstateClient({
      endpoint: `http://127.0.0.1:${port}`,
      namespace: "test",
      agentId: "claude:test:sess_1",
    });

    const resp = await client.claim("file://src/auth.ts", "write", "refactoring auth");
    expect(resp.conflicts?.has_conflict).toBe(true);
    expect(resp.conflicts?.conflicts?.[0].agent_id).toBe("agent-2");
    server.close();
  });
});
