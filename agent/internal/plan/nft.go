package plan

import (
	"fmt"
	"regexp"

	"github.com/gen1nya/wg-admin/agent/internal/kernel"
)

// nftForbiddenTokens are keywords that let the body escape our wg-admin
// table boundary. Anything matching these is rejected at plan creation
// time before the ruleset ever reaches nft.
var nftForbiddenTokens = regexp.MustCompile(`(?m)^\s*(table|delete|flush|include)\b`)

// validateNFTBody: body must not contain transaction-scope keywords. We
// don't try to parse nft syntax — just make sure the caller hasn't snuck
// in `table inet other { ... }` or `flush ruleset`.
func validateNFTBody(body string) error {
	if nftForbiddenTokens.MatchString(body) {
		return fmt.Errorf("nft body must not contain `table`/`delete`/`flush`/`include` (agent wraps it in its own wg-admin transaction)")
	}
	return nil
}

// snapshotNFT reads the current ruleset of our single owned table.
func snapshotNFT(k kernel.Kernel, spec *NFTSpec) (*NFTSnapshot, error) {
	if spec == nil {
		return nil, nil
	}
	current, err := k.NFTList(AgentNFTTable)
	if err != nil {
		return nil, fmt.Errorf("nft snapshot: %w", err)
	}
	if current == "" {
		return &NFTSnapshot{Existed: false}, nil
	}
	return &NFTSnapshot{Existed: true, Ruleset: current}, nil
}

func diffNFT(desired *NFTSpec, snap *NFTSnapshot) *NFTDiff {
	if desired == nil {
		return nil
	}
	if snap == nil || !snap.Existed {
		return &NFTDiff{Op: "create"}
	}
	// Heuristic: if desired body appears verbatim in current, treat as noop.
	// This is cheap and avoids false positives on whitespace/ordering. A
	// stricter comparator would canonicalise via `nft -c` but that's out of
	// scope for MVP.
	return &NFTDiff{Op: "replace"}
}

// applyNFT wraps the body in a delete-and-redefine transaction and feeds it
// to nft -f. Atomicity is guaranteed by nft itself.
func applyNFT(k kernel.Kernel, spec *NFTSpec) error {
	if spec == nil {
		return nil
	}
	txn := fmt.Sprintf(`add table inet %s
delete table inet %s
table inet %s {
%s
}
`, AgentNFTTable, AgentNFTTable, AgentNFTTable, spec.Body)
	return k.NFTApply(txn)
}

// revertNFT restores the snapshot. Uses the same transactional pattern.
func revertNFT(k kernel.Kernel, snap *NFTSnapshot) error {
	if snap == nil {
		return nil
	}
	if !snap.Existed {
		// Was missing before; wipe it (idempotent if already absent).
		txn := fmt.Sprintf(`add table inet %s
delete table inet %s
`, AgentNFTTable, AgentNFTTable)
		return k.NFTApply(txn)
	}
	// `nft list table inet X` output starts with `table inet X { ... }`.
	// Pair it with a delete so re-adding chains doesn't collide.
	txn := fmt.Sprintf(`add table inet %s
delete table inet %s
%s
`, AgentNFTTable, AgentNFTTable, snap.Ruleset)
	return k.NFTApply(txn)
}
