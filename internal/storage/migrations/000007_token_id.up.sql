-- Add a non-secret token identifier to allow indexed token lookup.
ALTER TABLE clients ADD COLUMN token_id TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_clients_token_id ON clients(token_id);
