export type Operation = "read" | "write" | "delete" | "rename" | "refactor";
export type ConflictMode = "block" | "warn" | "allow";
export interface Claim {
    claim_id: string;
    agent_id: string;
    resource_uri: string;
    operation: Operation;
    intent: string;
    projected_end?: string;
    ttl_seconds: number;
    parent_claim_id?: string;
    created_at: string;
}
export interface Conflict {
    claim_id: string;
    agent_id: string;
    resource: string;
    operation: Operation;
    intent: string;
    since: string;
}
export interface ConflictReport {
    has_conflict: boolean;
    conflicts?: Conflict[];
}
export interface ClaimResponse {
    claim_id: string;
    conflicts?: ConflictReport;
}
export interface ClientOptions {
    endpoint: string;
    namespace: string;
    agentId: string;
    defaultTTL?: number;
}
export type MailboxMessageType = "yield_request" | "yield_ack" | "escalation" | "all_clear" | "note" | "ping";
export interface MailboxMessage {
    messageId: string;
    from: string;
    to: string;
    type: MailboxMessageType;
    content: string;
    relatedClaimId?: string;
    relatedClaim?: Claim;
    sentAt: string;
    expiresAt: string;
    read: boolean;
}
export interface SendMessageRequest {
    to: string;
    type: MailboxMessageType;
    content: string;
    relatedClaimId?: string;
    ttlSeconds?: number;
}
export interface MailboxQueryOpts {
    unread?: boolean;
    includeClaims?: boolean;
    limit?: number;
}
