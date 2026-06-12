-- Per-peer WireGuard preshared key (symmetric, optional second factor on top
-- of the keypair). Stored so the rendered client .conf matches what the kernel
-- enforces — without it, a peer that has a PSK in the kernel gets a .conf with
-- no PresharedKey line and the handshake never completes.
ALTER TABLE peers ADD COLUMN preshared_key TEXT NOT NULL DEFAULT '';
