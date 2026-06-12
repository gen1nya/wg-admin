-- Prevent two peers on the same interface from claiming the same address.
-- A duplicate /32 silently hijacks the allowed-ip in the kernel (last writer
-- wins), cutting off the original client and leaving two DB rows that no
-- check ever reconciles. Catch-all mesh routes (0.0.0.0/0, ::/0) are exempt:
-- a mesh interface legitimately has several peers that each announce the
-- default route.
CREATE UNIQUE INDEX idx_peers_iface_addr ON peers (interface_id, address)
  WHERE address NOT IN ('0.0.0.0/0', '::/0');
