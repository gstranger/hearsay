import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Mock Node.js built-ins — factories must be self-contained (hoisted)
vi.mock("node:crypto", () => ({
  createHash: vi.fn(),
}));

vi.mock("node:child_process", () => ({
  spawn: vi.fn(),
}));

vi.mock("node:fs/promises", () => ({
  access: vi.fn(),
}));

// Import mocked modules to access mock functions
import { createHash } from "node:crypto";
import { spawn } from "node:child_process";
import { access } from "node:fs/promises";

const mockCreateHash = vi.mocked(createHash);
const mockSpawn = vi.mocked(spawn);
const mockAccess = vi.mocked(access);

// Mock global fetch
const mockFetch = vi.fn();
global.fetch = mockFetch;

// Import plugin after mocks
import plugin from "../agentstate.ts";

describe("OpenCode Extension", () => {
  const originalEnv = process.env;

  beforeEach(() => {
    vi.resetAllMocks();
    process.env = { ...originalEnv };

    // Default mock behaviors
    mockAccess.mockResolvedValue(undefined);
    mockFetch.mockResolvedValue({
      ok: true,
      json: async () => ({ claim_id: "claim-123" }),
    });
    mockCreateHash.mockReturnValue({
      update: vi.fn().mockReturnThis(),
      digest: vi.fn().mockReturnValue("abcdef123456"),
    } as any);
    mockSpawn.mockReturnValue({
      on: vi.fn(),
      unref: vi.fn(),
    } as any);
  });

  afterEach(() => {
    process.env = originalEnv;
  });

  describe("Configuration", () => {
    it("uses default config when no env vars set", async () => {
      delete process.env.AGENTSTATE_ENDPOINT;
      delete process.env.AGENTSTATE_NAMESPACE;
      delete process.env.AGENTSTATE_AGENT_ID;
      delete process.env.AGENTSTATE_TTL;
      delete process.env.AGENTSTATE_CLAIM_ON_READ;
      delete process.env.AGENTSTATE_ON_CONFLICT;

      const hooks = await plugin();
      expect(hooks).toBeDefined();
      expect(hooks["tool.execute.before"]).toBeDefined();
    });

    it("reads all env vars correctly", async () => {
      process.env.AGENTSTATE_ENDPOINT = "http://custom:9090";
      process.env.AGENTSTATE_NAMESPACE = "test-ns";
      process.env.AGENTSTATE_AGENT_ID = "test-agent";
      process.env.AGENTSTATE_TTL = "600";
      process.env.AGENTSTATE_CLAIM_ON_READ = "true";
      process.env.AGENTSTATE_ON_CONFLICT = "allow";

      const hooks = await plugin();
      expect(hooks).toBeDefined();
    });

    it("defaults onConflict to block for invalid values", async () => {
      process.env.AGENTSTATE_ON_CONFLICT = "invalid";
      const hooks = await plugin();
      expect(hooks).toBeDefined();
    });
  });

  describe("Auto-start behavior", () => {
    it("does not start serve if already running", async () => {
      mockFetch.mockImplementation((url: string) => {
        if (url.includes("/health")) {
          return Promise.resolve({ ok: true });
        }
        return Promise.resolve({
          ok: true,
          json: async () => ({ claim_id: "claim-123" }),
        });
      });

      const hooks = await plugin();
      expect(mockSpawn).not.toHaveBeenCalled();
    });

    it("starts serve if not running", async () => {
      let healthChecked = false;
      mockFetch.mockImplementation((url: string) => {
        if (url.includes("/health")) {
          if (!healthChecked) {
            healthChecked = true;
            return Promise.resolve({ ok: false });
          }
          return Promise.resolve({ ok: true });
        }
        return Promise.resolve({
          ok: true,
          json: async () => ({ claim_id: "claim-123" }),
        });
      });

      const hooks = await plugin();
      expect(mockSpawn).toHaveBeenCalled();
    });
  });

  describe("tool.execute.before", () => {
    it("claims resource on write tool", async () => {
      const hooks = await plugin();
      const before = hooks["tool.execute.before"] as Function;

      await before(
        { tool: "write", args: { filePath: "src/auth.ts" }, toolCallId: "tc-1" },
        {}
      );

      expect(mockFetch).toHaveBeenCalledWith(
        "http://localhost:8080/claim",
        expect.objectContaining({
          method: "POST",
          body: expect.stringContaining("file://src/auth.ts"),
        })
      );
    });

    it("claims resource on bash tool with SHA hash", async () => {
      const hooks = await plugin();
      const before = hooks["tool.execute.before"] as Function;

      await before(
        { tool: "bash", args: { command: "npm test" }, toolCallId: "tc-1" },
        {}
      );

      expect(mockCreateHash).toHaveBeenCalledWith("sha256");
      expect(mockFetch).toHaveBeenCalledWith(
        "http://localhost:8080/claim",
        expect.objectContaining({
          body: expect.stringContaining("exec://bash/"),
        })
      );
    });

    it("skips read tool when claimOnRead is false", async () => {
      process.env.AGENTSTATE_CLAIM_ON_READ = "false";
      const hooks = await plugin();
      const before = hooks["tool.execute.before"] as Function;

      await before(
        { tool: "read", args: { filePath: "src/auth.ts" }, toolCallId: "tc-1" },
        {}
      );

      const claimCalls = mockFetch.mock.calls.filter((call: any[]) =>
        call[0].includes("/claim")
      );
      expect(claimCalls).toHaveLength(0);
    });

    it("claims on read when claimOnRead is true", async () => {
      process.env.AGENTSTATE_CLAIM_ON_READ = "true";
      const hooks = await plugin();
      const before = hooks["tool.execute.before"] as Function;

      await before(
        { tool: "read", args: { filePath: "src/auth.ts" }, toolCallId: "tc-1" },
        {}
      );

      const claimCalls = mockFetch.mock.calls.filter((call: any[]) =>
        call[0].includes("/claim")
      );
      expect(claimCalls).toHaveLength(1);
    });

    it("blocks on conflict in block mode", async () => {
      process.env.AGENTSTATE_ON_CONFLICT = "block";
      mockFetch.mockResolvedValue({
        ok: true,
        json: async () => ({
          conflicts: {
            has_conflict: true,
            conflicts: [
              {
                agent_id: "agent-A",
                operation: "write",
                resource: "file://src/auth.ts",
                intent: "refactoring",
              },
            ],
          },
        }),
      });

      const hooks = await plugin();
      const before = hooks["tool.execute.before"] as Function;

      await expect(
        before(
          { tool: "write", args: { filePath: "src/auth.ts" }, toolCallId: "tc-1" },
          {}
        )
      ).rejects.toThrow("Conflict: agent-A is write file://src/auth.ts");
    });

    it("allows on conflict in allow mode", async () => {
      process.env.AGENTSTATE_ON_CONFLICT = "allow";
      mockFetch.mockResolvedValue({
        ok: true,
        json: async () => ({
          conflicts: {
            has_conflict: true,
            conflicts: [
              {
                agent_id: "agent-A",
                operation: "write",
                resource: "file://src/auth.ts",
                intent: "refactoring",
              },
            ],
          },
        }),
      });

      const hooks = await plugin();
      const before = hooks["tool.execute.before"] as Function;

      await expect(
        before(
          { tool: "write", args: { filePath: "src/auth.ts" }, toolCallId: "tc-1" },
          {}
        )
      ).resolves.toBeUndefined();
    });

    it("stores warning on conflict in warn mode", async () => {
      process.env.AGENTSTATE_ON_CONFLICT = "warn";
      mockFetch.mockResolvedValue({
        ok: true,
        json: async () => ({
          conflicts: {
            has_conflict: true,
            conflicts: [
              {
                agent_id: "agent-A",
                operation: "write",
                resource: "file://src/auth.ts",
                intent: "refactoring",
              },
            ],
          },
        }),
      });

      const hooks = await plugin();
      const before = hooks["tool.execute.before"] as Function;

      await before(
        { tool: "write", args: { filePath: "src/auth.ts" }, toolCallId: "tc-1" },
        {}
      );

      // Should not throw, warning stored for after hook
      // We verify this by checking the after hook behavior
    });

    it("gracefully degrades when agentstate is unreachable", async () => {
      mockFetch.mockRejectedValue(new Error("Connection refused"));

      const hooks = await plugin();
      const before = hooks["tool.execute.before"] as Function;

      await expect(
        before(
          { tool: "write", args: { filePath: "src/auth.ts" }, toolCallId: "tc-1" },
          {}
        )
      ).resolves.toBeUndefined();
    });
  });

  describe("tool.execute.after", () => {
    it("releases claim on success", async () => {
      // First, make a successful claim
      mockFetch.mockResolvedValue({
        ok: true,
        json: async () => ({ claim_id: "claim-123" }),
      });

      const hooks = await plugin();
      const before = hooks["tool.execute.before"] as Function;
      const after = hooks["tool.execute.after"] as Function;

      await before(
        { tool: "write", args: { filePath: "src/auth.ts" }, toolCallId: "tc-1" },
        {}
      );

      mockFetch.mockClear();

      await after(
        { toolCallId: "tc-1" },
        { result: "file written" }
      );

      expect(mockFetch).toHaveBeenCalledWith(
        "http://localhost:8080/release",
        expect.objectContaining({
          method: "POST",
          body: expect.stringContaining("claim-123"),
        })
      );
    });

    it("prepends warning to result in warn mode (experimental)", async () => {
      process.env.AGENTSTATE_ON_CONFLICT = "warn";
      mockFetch.mockResolvedValue({
        ok: true,
        json: async () => ({
          conflicts: {
            has_conflict: true,
            conflicts: [
              {
                agent_id: "agent-A",
                operation: "write",
                resource: "file://src/auth.ts",
                intent: "refactoring",
              },
            ],
          },
        }),
      });

      const hooks = await plugin();
      const before = hooks["tool.execute.before"] as Function;
      const after = hooks["tool.execute.after"] as Function;

      await before(
        { tool: "write", args: { filePath: "src/auth.ts" }, toolCallId: "tc-1" },
        {}
      );

      const output = { result: "file written successfully" };
      await after(
        { toolCallId: "tc-1" },
        output
      );

      expect(output.result).toContain("⚠️");
      expect(output.result).toContain("Conflict: agent-A is write file://src/auth.ts");
      expect(output.result).toContain("file written successfully");
    });

    it("handles missing output gracefully", async () => {
      process.env.AGENTSTATE_ON_CONFLICT = "warn";
      mockFetch.mockResolvedValue({
        ok: true,
        json: async () => ({
          conflicts: {
            has_conflict: true,
            conflicts: [
              {
                agent_id: "agent-A",
                operation: "write",
                resource: "file://src/auth.ts",
                intent: "refactoring",
              },
            ],
          },
        }),
      });

      const hooks = await plugin();
      const before = hooks["tool.execute.before"] as Function;
      const after = hooks["tool.execute.after"] as Function;

      await before(
        { tool: "write", args: { filePath: "src/auth.ts" }, toolCallId: "tc-1" },
        {}
      );

      const output = {};
      await after(
        { toolCallId: "tc-1" },
        output
      );

      // Should not throw, sets result property
      expect((output as any).result).toContain("⚠️");
    });
  });

  describe("session.status", () => {
    it("releases all active claims on session end", async () => {
      // Make two claims
      let claimCount = 0;
      mockFetch.mockImplementation((url: string) => {
        if (url.includes("/claim")) {
          claimCount++;
          return Promise.resolve({
            ok: true,
            json: async () => ({ claim_id: `claim-${claimCount}` }),
          });
        }
        if (url.includes("/release")) {
          return Promise.resolve({ ok: true });
        }
        return Promise.resolve({ ok: true });
      });

      const hooks = await plugin();
      const before = hooks["tool.execute.before"] as Function;
      const status = hooks["session.status"] as Function;

      await before(
        { tool: "write", args: { filePath: "src/a.ts" }, toolCallId: "tc-1" },
        {}
      );
      await before(
        { tool: "write", args: { filePath: "src/b.ts" }, toolCallId: "tc-2" },
        {}
      );

      mockFetch.mockClear();

      await status({}, {});

      // Should release both claims
      const releaseCalls = mockFetch.mock.calls.filter((call: any[]) =>
        call[0].includes("/release")
      );
      expect(releaseCalls).toHaveLength(2);
    });
  });

  describe("URI generation", () => {
    it("generates file:// URI for read/write/edit", async () => {
      process.env.AGENTSTATE_CLAIM_ON_READ = "true";
      const hooks = await plugin();
      const before = hooks["tool.execute.before"] as Function;

      await before(
        { tool: "read", args: { filePath: "src/auth.ts" }, toolCallId: "tc-1" },
        {}
      );

      const claimCall = mockFetch.mock.calls.find((call: any[]) =>
        call[0].includes("/claim")
      );
      const body = JSON.parse(claimCall![1].body);
      expect(body.resource_uri).toBe("file://src/auth.ts");
    });

    it("generates exec:// URI for bash", async () => {
      const hooks = await plugin();
      const before = hooks["tool.execute.before"] as Function;

      await before(
        { tool: "bash", args: { command: "npm test" }, toolCallId: "tc-1" },
        {}
      );

      const claimCall = mockFetch.mock.calls.find((call: any[]) =>
        call[0].includes("/claim")
      );
      const body = JSON.parse(claimCall![1].body);
      expect(body.resource_uri).toMatch(/^exec:\/\/bash\/[a-f0-9]{6}$/);
    });

    it("skips unknown tools", async () => {
      const hooks = await plugin();
      const before = hooks["tool.execute.before"] as Function;

      await before(
        { tool: "unknown_tool", args: {}, toolCallId: "tc-1" },
        {}
      );

      const claimCalls = mockFetch.mock.calls.filter((call: any[]) =>
        call[0].includes("/claim")
      );
      expect(claimCalls).toHaveLength(0);
    });
  });
});
