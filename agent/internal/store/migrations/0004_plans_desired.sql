-- Plan stores the desired target state explicitly (stateless flow: UI sends
-- the whole target in POST /plan, agent stores + diffs + applies).
-- The pre-existing `diff` column keeps the human-readable add/remove summary
-- for UI display; `desired` is the machine-authoritative target for apply.
ALTER TABLE plans ADD COLUMN desired TEXT NOT NULL DEFAULT '';
