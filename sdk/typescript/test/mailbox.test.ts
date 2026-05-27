import { describe, it, expect, vi, beforeEach } from "vitest";
import { HearsayClient } from "../src/client.js";

const mockFetch = vi.fn();
global.fetch = mockFetch;

describe("Mailbox", () => {
  const client = new HearsayClient({
    endpoint: "http://localhost:8080",
    namespace: "test-ns",
    agentId: "agent-A",
  });

  beforeEach(() => {
    vi.resetAllMocks();
  });

  it("sendMessage posts to /message", async () => {
    mockFetch.mockResolvedValue({
      ok: true,
      json: async () => ({ message_id: "msg-123" }),
    });

    const result = await client.sendMessage({
      to: "agent-B",
      type: "note",
      content: "hello",
    });

    expect(result.messageId).toBe("msg-123");
    expect(mockFetch).toHaveBeenCalledWith(
      "http://localhost:8080/message",
      expect.objectContaining({
        method: "POST",
        body: expect.stringContaining("hello"),
      })
    );
  });

  it("getMailbox queries with agent_id", async () => {
    mockFetch.mockResolvedValue({
      ok: true,
      json: async () => [
        { message_id: "msg-123", from: "agent-B", content: "hi" },
      ],
    });

    const msgs = await client.getMailbox({ unread: true });

    expect(msgs).toHaveLength(1);
    expect(mockFetch).toHaveBeenCalledWith(
      expect.stringContaining("/mailbox?")
    );
  });

  it("markRead posts to /message/read", async () => {
    mockFetch.mockResolvedValue({ ok: true, json: async () => ({ ok: true }) });

    await client.markRead("msg-123");

    expect(mockFetch).toHaveBeenCalledWith(
      "http://localhost:8080/message/read",
      expect.objectContaining({ method: "POST" })
    );
  });

  it("archiveMessage posts to /message/archive", async () => {
    mockFetch.mockResolvedValue({ ok: true, json: async () => ({ ok: true }) });

    await client.archiveMessage("msg-123");

    expect(mockFetch).toHaveBeenCalledWith(
      "http://localhost:8080/message/archive",
      expect.objectContaining({ method: "POST" })
    );
  });
});
