export class AgentstateClient {
    opts;
    constructor(opts) {
        this.opts = {
            defaultTTL: 300,
            ...opts,
        };
    }
    async post(path, body) {
        const resp = await fetch(`${this.opts.endpoint}${path}`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
        });
        return resp;
    }
    async get(path) {
        return fetch(`${this.opts.endpoint}${path}`);
    }
    async parseJSON(resp) {
        if (!resp.ok) {
            const text = await resp.text().catch(() => "unknown error");
            throw new Error(`HTTP ${resp.status}: ${text}`);
        }
        return (await resp.json());
    }
    async claim(resourceURI, operation, intent, ttl) {
        const resp = await this.post("/claim", {
            namespace: this.opts.namespace,
            agent_id: this.opts.agentId,
            resource_uri: resourceURI,
            operation,
            intent,
            ttl_seconds: ttl ?? this.opts.defaultTTL,
        });
        // 409 Conflict or 423 Locked are valid responses carrying the conflict report
        if (resp.status === 409 || resp.status === 423) {
            return (await resp.json());
        }
        return this.parseJSON(resp);
    }
    async release(claimId, outcome) {
        const resp = await this.post("/release", {
            namespace: this.opts.namespace,
            claim_id: claimId,
            outcome,
        });
        if (!resp.ok) {
            const text = await resp.text().catch(() => "unknown error");
            throw new Error(`HTTP ${resp.status}: ${text}`);
        }
    }
    async heartbeat(claimId) {
        const resp = await this.post("/heartbeat", {
            namespace: this.opts.namespace,
            claim_id: claimId,
        });
        if (!resp.ok) {
            const text = await resp.text().catch(() => "unknown error");
            throw new Error(`HTTP ${resp.status}: ${text}`);
        }
    }
    async query(resourcePattern, agentId) {
        const params = new URLSearchParams({ namespace: this.opts.namespace });
        if (resourcePattern)
            params.set("resource", resourcePattern);
        if (agentId)
            params.set("agent", agentId);
        const resp = await this.get(`/claims?${params.toString()}`);
        return this.parseJSON(resp);
    }
    async check(resourceURI, operation) {
        const params = new URLSearchParams({
            namespace: this.opts.namespace,
            resource: resourceURI,
            operation,
        });
        const resp = await this.get(`/check?${params.toString()}`);
        return this.parseJSON(resp);
    }
    async sendMessage(msg) {
        const resp = await fetch(`${this.opts.endpoint}/message`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
                namespace: this.opts.namespace,
                from: this.opts.agentId,
                to: msg.to,
                type: msg.type,
                content: msg.content,
                related_claim_id: msg.relatedClaimId,
                ttl_seconds: msg.ttlSeconds ?? this.opts.defaultTTL,
            }),
        });
        if (!resp.ok) {
            throw new Error(`sendMessage failed: ${resp.status}`);
        }
        const data = await resp.json();
        return { messageId: data.message_id };
    }
    async getMailbox(opts = {}) {
        const params = new URLSearchParams({
            namespace: this.opts.namespace,
            agent_id: this.opts.agentId,
        });
        if (opts.unread)
            params.set("unread", "true");
        if (opts.includeClaims)
            params.set("include_claims", "true");
        if (opts.limit)
            params.set("limit", opts.limit.toString());
        const resp = await fetch(`${this.opts.endpoint}/mailbox?${params}`);
        if (!resp.ok) {
            throw new Error(`getMailbox failed: ${resp.status}`);
        }
        const data = await resp.json();
        return data.map((m) => ({
            messageId: String(m.message_id ?? m.messageId),
            from: String(m.from),
            to: String(m.to),
            type: m.type,
            content: String(m.content),
            relatedClaimId: m.related_claim_id ? String(m.related_claim_id) : undefined,
            relatedClaim: m.related_claim,
            sentAt: String(m.sent_at ?? m.sentAt),
            expiresAt: String(m.expires_at ?? m.expiresAt),
            read: Boolean(m.read),
        }));
    }
    async markRead(messageId) {
        const resp = await fetch(`${this.opts.endpoint}/message/read`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ namespace: this.opts.namespace, message_id: messageId }),
        });
        if (!resp.ok) {
            throw new Error(`markRead failed: ${resp.status}`);
        }
    }
    async archiveMessage(messageId) {
        const resp = await fetch(`${this.opts.endpoint}/message/archive`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ namespace: this.opts.namespace, message_id: messageId }),
        });
        if (!resp.ok) {
            throw new Error(`archiveMessage failed: ${resp.status}`);
        }
    }
}
