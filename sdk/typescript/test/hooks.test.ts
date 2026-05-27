import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { createHooks, type HookOptions } from "../src/hooks.js";
import { HearsayClient } from "../src/client.js";

describe("createHooks", () => {
  const mockClaim = vi.fn();
  const mockRelease = vi.fn();
  const mockHeartbeat = vi.fn();

  beforeEach(() => {
    mockClaim.mockReset();
    mockRelease.mockReset();
    mockHeartbeat.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("allows tool use when no conflict", async () => {
    mockClaim.mockResolvedValue({ claim_id: "c1" });

    const hooks = createHooks({
      endpoint: "http://localhost:8080",
      namespace: "test",
      agentId: "claude:test:s1",
      claimOnRead: true,
    });

    (hooks as any).client = { claim: mockClaim, release: mockRelease, heartbeat: mockHeartbeat };

    const result = await hooks.preToolUse("Read", { file_path: "src/auth.ts" });
    expect(result.permissionDecision).toBe("allow");
    expect(mockClaim).toHaveBeenCalledWith("file://src/auth.ts", "read", expect.any(String));
  });

  it("warns on conflict for write operations by default", async () => {
    mockClaim.mockResolvedValue({
      claim_id: "c2",
      conflicts: {
        has_conflict: true,
        conflicts: [{
          claim_id: "c3",
          agent_id: "agent-2",
          resource: "file://src/auth.ts",
          operation: "write",
          intent: "fixing bug",
          since: "2026-05-21T00:00:00Z",
        }],
      },
    });

    const hooks = createHooks({
      endpoint: "http://localhost:8080",
      namespace: "test",
      agentId: "claude:test:s1",
    });
    (hooks as any).client = { claim: mockClaim, release: mockRelease, heartbeat: mockHeartbeat };

    const result = await hooks.preToolUse("Write", { file_path: "src/auth.ts", content: "..." });
    expect(result.permissionDecision).toBe("allow");
    expect(result.agentMessage).toContain("⚠️");
    expect(result.agentMessage).toContain("agent-2");
  });

  it("blocks on conflict when onConflict is 'block'", async () => {
    mockClaim.mockResolvedValue({
      claim_id: "c2",
      conflicts: {
        has_conflict: true,
        conflicts: [{
          claim_id: "c3",
          agent_id: "agent-2",
          resource: "file://src/auth.ts",
          operation: "write",
          intent: "fixing bug",
          since: "2026-05-21T00:00:00Z",
        }],
      },
    });

    const hooks = createHooks({
      endpoint: "http://localhost:8080",
      namespace: "test",
      agentId: "claude:test:s1",
      onConflict: "block",
    });
    (hooks as any).client = { claim: mockClaim, release: mockRelease, heartbeat: mockHeartbeat };

    const result = await hooks.preToolUse("Write", { file_path: "src/auth.ts", content: "..." });
    expect(result.permissionDecision).toBe("deny");
    expect(result.agentMessage).toContain("Conflict:");
    expect(result.agentMessage).not.toContain("⚠️");
    expect(result.agentMessage).toContain("agent-2");
  });

  it("allows silently on conflict when onConflict is 'allow'", async () => {
    mockClaim.mockResolvedValue({
      claim_id: "c2",
      conflicts: {
        has_conflict: true,
        conflicts: [{
          claim_id: "c3",
          agent_id: "agent-2",
          resource: "file://src/auth.ts",
          operation: "write",
          intent: "fixing bug",
          since: "2026-05-21T00:00:00Z",
        }],
      },
    });

    const hooks = createHooks({
      endpoint: "http://localhost:8080",
      namespace: "test",
      agentId: "claude:test:s1",
      onConflict: "allow",
    });
    (hooks as any).client = { claim: mockClaim, release: mockRelease, heartbeat: mockHeartbeat };

    const result = await hooks.preToolUse("Write", { file_path: "src/auth.ts", content: "..." });
    expect(result.permissionDecision).toBe("allow");
    expect(result.agentMessage).toBeUndefined();
  });

  it("releases claim in postToolUse", async () => {
    mockClaim.mockResolvedValue({ claim_id: "c4" });
    mockRelease.mockResolvedValue(undefined);

    const hooks = createHooks({
      endpoint: "http://localhost:8080",
      namespace: "test",
      agentId: "claude:test:s1",
    });
    (hooks as any).client = { claim: mockClaim, release: mockRelease, heartbeat: mockHeartbeat };

    await hooks.preToolUse("Write", { file_path: "src/auth.ts", content: "..." });
    await hooks.postToolUse("Write", { file_path: "src/auth.ts", content: "..." }, { ok: true });

    expect(mockRelease).toHaveBeenCalledWith("c4", "succeeded");
  });

  it("starts heartbeat on successful claim", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mockClaim.mockResolvedValue({ claim_id: "c1" });
    mockHeartbeat.mockResolvedValue(undefined);

    const hooks = createHooks({
      endpoint: "http://localhost:8080",
      namespace: "test",
      agentId: "claude:test:s1",
      autoHeartbeat: true,
      defaultTTL: 60,
    });
    (hooks as any).client = { claim: mockClaim, release: mockRelease, heartbeat: mockHeartbeat };

    await hooks.preToolUse("Write", { file_path: "x.ts" });
    expect(mockHeartbeat).not.toHaveBeenCalled();

    vi.advanceTimersByTime(30_000);
    await Promise.resolve(); // flush first async layer
    await Promise.resolve(); // flush catch/if continuation
    expect(mockHeartbeat).toHaveBeenCalledWith("c1");

    vi.advanceTimersByTime(30_000);
    await Promise.resolve();
    await Promise.resolve();
    expect(mockHeartbeat).toHaveBeenCalledTimes(2);
  });

  it("stops heartbeat on postToolUse", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mockClaim.mockResolvedValue({ claim_id: "c1" });
    mockHeartbeat.mockResolvedValue(undefined);
    mockRelease.mockResolvedValue(undefined);

    const hooks = createHooks({
      endpoint: "http://localhost:8080",
      namespace: "test",
      agentId: "claude:test:s1",
      autoHeartbeat: true,
      defaultTTL: 60,
    });
    (hooks as any).client = { claim: mockClaim, release: mockRelease, heartbeat: mockHeartbeat };

    await hooks.preToolUse("Write", { file_path: "x.ts" });
    await hooks.postToolUse("Write", { file_path: "x.ts" }, {});

    mockHeartbeat.mockClear();
    vi.advanceTimersByTime(60_000);
    await Promise.resolve();
    expect(mockHeartbeat).not.toHaveBeenCalled();
  });

  it("warns on heartbeat failure but does not throw", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
    mockClaim.mockResolvedValue({ claim_id: "c1" });
    mockHeartbeat.mockImplementation(() => Promise.reject(new Error("network")));

    const hooks = createHooks({
      endpoint: "http://localhost:8080",
      namespace: "test",
      agentId: "claude:test:s1",
      autoHeartbeat: true,
      defaultTTL: 60,
    });
    (hooks as any).client = { claim: mockClaim, release: mockRelease, heartbeat: mockHeartbeat };

    await hooks.preToolUse("Write", { file_path: "x.ts" });

    vi.advanceTimersByTime(30_000);
    await Promise.resolve();
    expect(hooks._heartbeatCount()).toBe(1);

    vi.advanceTimersByTime(30_000);
    await Promise.resolve();
    expect(mockHeartbeat).toHaveBeenCalledTimes(2);

    warnSpy.mockRestore();
  });

  it("does not start heartbeat when autoHeartbeat is false", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mockClaim.mockResolvedValue({ claim_id: "c1" });
    mockHeartbeat.mockResolvedValue(undefined);

    const hooks = createHooks({
      endpoint: "http://localhost:8080",
      namespace: "test",
      agentId: "claude:test:s1",
      autoHeartbeat: false,
      defaultTTL: 60,
    });
    (hooks as any).client = { claim: mockClaim, release: mockRelease, heartbeat: mockHeartbeat };

    await hooks.preToolUse("Write", { file_path: "x.ts" });
    vi.advanceTimersByTime(60_000);
    await Promise.resolve();
    expect(mockHeartbeat).not.toHaveBeenCalled();
  });
});
