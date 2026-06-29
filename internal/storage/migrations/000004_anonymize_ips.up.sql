ALTER TABLE clients DROP COLUMN current_ip;

UPDATE proxy_events  SET source_ip = '';
UPDATE auth_failures SET source_ip = '';

DELETE FROM ip_bans;
