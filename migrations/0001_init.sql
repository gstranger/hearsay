-- 0001_init.sql
-- Initial hearsay schema for Cloudflare D1

CREATE TABLE IF NOT EXISTS namespaces (
    id TEXT PRIMARY KEY,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS messages (
    namespace TEXT NOT NULL,
    offset INTEGER NOT NULL,
    seq INTEGER NOT NULL,
    type TEXT NOT NULL,
    agent_id TEXT,
    payload TEXT,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (namespace, offset)
);

CREATE INDEX IF NOT EXISTS idx_messages_type ON messages(namespace, type);
CREATE INDEX IF NOT EXISTS idx_messages_agent ON messages(namespace, agent_id);
CREATE INDEX IF NOT EXISTS idx_messages_seq ON messages(namespace, seq);

CREATE TABLE IF NOT EXISTS mailbox_messages (
    message_id       TEXT PRIMARY KEY,
    namespace        TEXT NOT NULL,
    from_agent       TEXT NOT NULL,
    to_agent         TEXT NOT NULL,
    message_type     TEXT NOT NULL,
    content          TEXT NOT NULL,
    related_claim_id TEXT,
    read             BOOLEAN DEFAULT FALSE,
    archived         BOOLEAN DEFAULT FALSE,
    created_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at       DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_mailbox_inbox ON mailbox_messages(namespace, to_agent, read, archived);
CREATE INDEX IF NOT EXISTS idx_mailbox_claim ON mailbox_messages(related_claim_id);
CREATE INDEX IF NOT EXISTS idx_mailbox_expires ON mailbox_messages(expires_at);

CREATE TABLE IF NOT EXISTS a2a_tasks (
    id              TEXT PRIMARY KEY,
    session_id      TEXT,
    state           TEXT NOT NULL,
    status_message  TEXT,
    status_time     DATETIME NOT NULL,
    claim_id        TEXT,
    namespace       TEXT NOT NULL,
    metadata        TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_a2a_tasks_ns ON a2a_tasks(namespace);

CREATE TABLE IF NOT EXISTS a2a_task_history (
    task_id     TEXT NOT NULL,
    seq         INTEGER NOT NULL,
    role        TEXT NOT NULL,
    parts       TEXT NOT NULL,
    metadata    TEXT,
    timestamp   DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, seq)
);

CREATE TABLE IF NOT EXISTS a2a_artifacts (
    task_id     TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT,
    parts       TEXT NOT NULL,
    index_num   INTEGER,
    append      BOOLEAN DEFAULT FALSE,
    last_chunk  BOOLEAN DEFAULT FALSE,
    metadata    TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, name, index_num)
);

CREATE TABLE IF NOT EXISTS audit_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace TEXT NOT NULL,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    level TEXT NOT NULL,
    event_type TEXT NOT NULL,
    agent_id TEXT,
    resource_uri TEXT,
    outcome TEXT,
    metadata TEXT
);

CREATE INDEX IF NOT EXISTS idx_audit_namespace ON audit_events(namespace);
CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_events(namespace, timestamp);