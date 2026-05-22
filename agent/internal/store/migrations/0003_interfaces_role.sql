-- Distinguish client-facing interfaces from mesh/exit tunnels.
-- Tier-1: only 'clients' accepts peer CRUD. 'mesh' is read-only (monitoring only).
-- Tier-2 may split 'mesh' into 'mesh'/'exit' once exits table is the source of truth.
ALTER TABLE interfaces ADD COLUMN role TEXT NOT NULL DEFAULT 'clients'
  CHECK (role IN ('clients','mesh'));
