import { AgentstateClient, type ClientOptions } from "./client.js";
import type { ConflictMode } from "./types.js";
export interface HookOptions extends ClientOptions {
    autoHeartbeat?: boolean;
    claimOnRead?: boolean;
    onConflict?: ConflictMode;
}
export interface PreToolUseResult {
    permissionDecision: "allow" | "deny";
    agentMessage?: string;
}
export declare function createHooks(opts: HookOptions): {
    client: AgentstateClient;
    preToolUse(toolName: string, input: Record<string, unknown>): Promise<PreToolUseResult>;
    postToolUse(toolName: string, input: Record<string, unknown>, _result: unknown): Promise<void>;
    sessionStart(): Promise<void>;
    sessionEnd(): Promise<void>;
    _activeClaimsCount: () => number;
    _heartbeatCount: () => number;
};
