CREATE TABLE IF NOT EXISTS blocked_domains (
    domain   TEXT NOT NULL,
    category TEXT NOT NULL,
    PRIMARY KEY (domain, category)
);
CREATE INDEX IF NOT EXISTS idx_blocked_domains_domain ON blocked_domains(domain);
