CREATE TABLE IF NOT EXISTS domain_stats_daily (
    day             INTEGER NOT NULL,
    client_id       INTEGER NOT NULL DEFAULT 0,
    domain          TEXT    NOT NULL,
    requests        INTEGER NOT NULL DEFAULT 0,
    bytes_in        INTEGER NOT NULL DEFAULT 0,
    bytes_out       INTEGER NOT NULL DEFAULT 0,
    sum_duration_ms INTEGER NOT NULL DEFAULT 0,
    sum_connect_ms  INTEGER NOT NULL DEFAULT 0,
    connect_count   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, client_id, domain)
);

CREATE INDEX IF NOT EXISTS idx_domain_stats_daily_client_day ON domain_stats_daily(client_id, day);
CREATE INDEX IF NOT EXISTS idx_domain_stats_daily_day ON domain_stats_daily(day);

INSERT INTO domain_stats_daily (day, client_id, domain, requests, bytes_in, bytes_out, sum_duration_ms, sum_connect_ms, connect_count)
SELECT
    ts - (ts % 86400),
    COALESCE(client_id, 0),
    CASE WHEN rtrim(target_host, '0123456789') LIKE '%:' AND rtrim(target_host, '0123456789') <> target_host
         THEN substr(target_host, 1, length(rtrim(target_host, '0123456789')) - 1)
         ELSE target_host END AS dom,
    COUNT(*),
    SUM(bytes_in),
    SUM(bytes_out),
    SUM(duration_ms),
    SUM(connect_ms),
    SUM(CASE WHEN connect_ms > 0 THEN 1 ELSE 0 END)
FROM proxy_events
WHERE outcome = 'ok'
GROUP BY 1, 2, 3
ON CONFLICT(day, client_id, domain) DO NOTHING;
