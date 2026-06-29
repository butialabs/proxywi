-- Critical indexes for dashboard, logs and origin analytics.
CREATE INDEX IF NOT EXISTS idx_proxy_events_user_ts ON proxy_events(user_id, ts);
CREATE INDEX IF NOT EXISTS idx_proxy_events_client_ts ON proxy_events(client_id, ts);
CREATE INDEX IF NOT EXISTS idx_proxy_events_origin_ts ON proxy_events(source_ip, ts);
CREATE INDEX IF NOT EXISTS idx_proxy_events_outcome_ts ON proxy_events(outcome, ts);
