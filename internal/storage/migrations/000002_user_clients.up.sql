CREATE TABLE IF NOT EXISTS user_clients (
    user_id   INTEGER NOT NULL,
    client_id INTEGER NOT NULL,
    PRIMARY KEY (user_id, client_id),
    FOREIGN KEY (user_id)   REFERENCES users(id)   ON DELETE CASCADE,
    FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_user_clients_user   ON user_clients(user_id);
CREATE INDEX IF NOT EXISTS idx_user_clients_client ON user_clients(client_id);
