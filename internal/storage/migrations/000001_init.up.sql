CREATE TABLE IF NOT EXISTS admins (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    email         TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_sessions (
    id          TEXT PRIMARY KEY,
    admin_id    INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    FOREIGN KEY (admin_id) REFERENCES admins(id)
);

CREATE TABLE IF NOT EXISTS clients (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL,
    token_hash    TEXT NOT NULL,
    last_seen_at  INTEGER NOT NULL DEFAULT 0,
    current_ip    TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_clients_last_seen ON clients(last_seen_at);

CREATE TABLE IF NOT EXISTS users (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    username             TEXT NOT NULL UNIQUE,
    password_hash        TEXT NOT NULL,
    allowed_source_cidrs TEXT NOT NULL DEFAULT '',
    created_at           INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS metric_samples (
    client_id    INTEGER NOT NULL,
    bucket_ts    INTEGER NOT NULL,
    bytes_in     INTEGER NOT NULL DEFAULT 0,
    bytes_out    INTEGER NOT NULL DEFAULT 0,
    active_conns INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (client_id, bucket_ts)
);

CREATE INDEX IF NOT EXISTS idx_metric_samples_ts ON metric_samples(bucket_ts);

CREATE TABLE IF NOT EXISTS proxy_events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ts           INTEGER NOT NULL,
    user_id      INTEGER,
    client_id    INTEGER,
    target_host  TEXT NOT NULL,
    source_ip    TEXT NOT NULL DEFAULT '',
    protocol     TEXT NOT NULL DEFAULT '',
    outcome      TEXT NOT NULL DEFAULT 'ok',
    bytes_in     INTEGER NOT NULL DEFAULT 0,
    bytes_out    INTEGER NOT NULL DEFAULT 0,
    duration_ms  INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_proxy_events_ts ON proxy_events(ts);

CREATE TABLE IF NOT EXISTS auth_failures (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    ts                 INTEGER NOT NULL,
    source_ip          TEXT NOT NULL,
    username_attempted TEXT NOT NULL,
    protocol           TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_auth_failures_ip_ts ON auth_failures(source_ip, ts);

CREATE TABLE IF NOT EXISTS ip_bans (
    source_ip     TEXT PRIMARY KEY,
    banned_until  INTEGER NOT NULL,
    reason        TEXT NOT NULL DEFAULT '',
    failure_count INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_ip_bans_until ON ip_bans(banned_until);
