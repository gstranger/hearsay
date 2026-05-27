import { describe, it, expect } from "vitest";
import { toolToURI } from "../src/uri.js";

describe("toolToURI", () => {
  it("maps Read to file:// URI", () => {
    expect(toolToURI("Read", { file_path: "src/auth.ts" })).toBe("file://src/auth.ts");
  });

  it("maps Write to file:// URI", () => {
    expect(toolToURI("Write", { file_path: "src/auth.ts", content: "..." })).toBe("file://src/auth.ts");
  });

  it("maps Bash to exec:// URI", () => {
    expect(toolToURI("Bash", { command: "npm test", timeout: 30 })).toBe("exec://bash/328e12");
  });

  it("maps Edit to file:// URI", () => {
    expect(toolToURI("Edit", { file_path: "src/auth.ts", old_string: "x", new_string: "y" })).toBe("file://src/auth.ts");
  });

  it("maps Glob to file:// pattern URI", () => {
    expect(toolToURI("Glob", { pattern: "src/**/*.ts" })).toBe("file://src/**/*.ts");
  });

  it("returns null for unsupported tools", () => {
    expect(toolToURI("Grep", { pattern: "foo" })).toBeNull();
  });
});
