CREATE TABLE IF NOT EXISTS token_sessions (
    id          TEXT PRIMARY KEY,
    client_id   INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    FOREIGN KEY (client_id) REFERENCES clients(id)
);

CREATE INDEX IF NOT EXISTS idx_token_sessions_expires ON token_sessions(expires_at);
