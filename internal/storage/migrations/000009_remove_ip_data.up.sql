CREATE TABLE clients_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL,
    token_hash    TEXT NOT NULL,
    token_id      TEXT NOT NULL DEFAULT '',
    agent_version TEXT,
    last_seen_at  INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);
INSERT INTO clients_new (id, name, token_hash, token_id, last_seen_at, created_at)
    SELECT id, name, token_hash, COALESCE(NULLIF(token_id, ''), printf('legacy-%d', id)), last_seen_at, created_at FROM clients;
DROP TABLE clients;
ALTER TABLE clients_new RENAME TO clients;

CREATE INDEX IF NOT EXISTS idx_clients_last_seen ON clients(last_seen_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_clients_token_id ON clients(token_id);

DROP INDEX IF EXISTS idx_proxy_events_origin_ts;
DROP INDEX IF EXISTS idx_auth_failures_ip_ts;

CREATE TABLE proxy_events_new (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ts           INTEGER NOT NULL,
    user_id      INTEGER,
    client_id    INTEGER,
    target_host  TEXT NOT NULL,
    protocol     TEXT NOT NULL DEFAULT '',
    outcome      TEXT NOT NULL DEFAULT 'ok',
    bytes_in     INTEGER NOT NULL DEFAULT 0,
    bytes_out    INTEGER NOT NULL DEFAULT 0,
    duration_ms  INTEGER NOT NULL DEFAULT 0
);
INSERT INTO proxy_events_new (id, ts, user_id, client_id, target_host, protocol, outcome, bytes_in, bytes_out, duration_ms)
    SELECT id, ts, user_id, client_id, target_host, protocol, outcome, bytes_in, bytes_out, duration_ms FROM proxy_events;
DROP TABLE proxy_events;
ALTER TABLE proxy_events_new RENAME TO proxy_events;

CREATE INDEX IF NOT EXISTS idx_proxy_events_ts ON proxy_events(ts);
CREATE INDEX IF NOT EXISTS idx_proxy_events_user_ts ON proxy_events(user_id, ts);
CREATE INDEX IF NOT EXISTS idx_proxy_events_client_ts ON proxy_events(client_id, ts);
CREATE INDEX IF NOT EXISTS idx_proxy_events_outcome_ts ON proxy_events(outcome, ts);

CREATE TABLE auth_failures_new (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    ts                 INTEGER NOT NULL,
    username_attempted TEXT NOT NULL,
    protocol           TEXT NOT NULL
);
INSERT INTO auth_failures_new (id, ts, username_attempted, protocol)
    SELECT id, ts, username_attempted, protocol FROM auth_failures;
DROP TABLE auth_failures;
ALTER TABLE auth_failures_new RENAME TO auth_failures;

CREATE TABLE users_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    INTEGER NOT NULL
);
INSERT INTO users_new (id, username, password_hash, created_at)
    SELECT id, username, password_hash, created_at FROM users;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;

DROP TABLE IF EXISTS user_clients;
DROP INDEX IF EXISTS idx_user_clients_user;
DROP INDEX IF EXISTS idx_user_clients_client;

DROP TABLE IF EXISTS ip_bans;
DROP TABLE IF EXISTS ip_allowlist;
