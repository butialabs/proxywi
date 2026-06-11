CREATE TABLE IF NOT EXISTS ip_allowlist (
    ip         TEXT PRIMARY KEY,
    reason     TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
