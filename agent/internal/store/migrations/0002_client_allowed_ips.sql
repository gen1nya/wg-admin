ALTER TABLE interfaces
  ADD COLUMN client_allowed_ips TEXT NOT NULL DEFAULT '0.0.0.0/0';
