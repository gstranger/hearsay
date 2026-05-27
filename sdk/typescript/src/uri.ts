import { createHash } from "node:crypto";

export function toolToURI(toolName: string, input: Record<string, unknown>): string | null {
  switch (toolName) {
    case "Read":
    case "Write":
    case "Edit": {
      const path = input.file_path ?? input.path;
      if (typeof path !== "string") return null;
      return `file://${path}`;
    }
    case "Bash": {
      const command = input.command;
      if (typeof command !== "string") return null;
      const hash = createHash("sha256").update(command).digest("hex").slice(0, 6);
      return `exec://bash/${hash}`;
    }
    case "Glob": {
      const pattern = input.pattern;
      if (typeof pattern !== "string") return null;
      return `file://${pattern}`;
    }
    default:
      return null;
  }
}
