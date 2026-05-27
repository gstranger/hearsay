import type { ClaimResponse, ConflictReport, Operation, SendMessageRequest, MailboxQueryOpts, MailboxMessage } from "./types.js";
export interface ClientOptions {
    endpoint: string;
    namespace: string;
    agentId: string;
    defaultTTL?: number;
}
export declare class AgentstateClient {
    private opts;
    constructor(opts: ClientOptions);
    private post;
    private get;
    private parseJSON;
    claim(resourceURI: string, operation: Operation, intent: string, ttl?: number): Promise<ClaimResponse>;
    release(claimId: string, outcome: string): Promise<void>;
    heartbeat(claimId: string): Promise<void>;
    query(resourcePattern?: string, agentId?: string): Promise<any[]>;
    check(resourceURI: string, operation: Operation): Promise<ConflictReport>;
    sendMessage(msg: SendMessageRequest): Promise<{
        messageId: string;
    }>;
    getMailbox(opts?: MailboxQueryOpts): Promise<MailboxMessage[]>;
    markRead(messageId: string): Promise<void>;
    archiveMessage(messageId: string): Promise<void>;
}
