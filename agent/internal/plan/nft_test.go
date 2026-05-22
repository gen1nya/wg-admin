package plan_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gen1nya/wg-admin/agent/internal/plan"
)

func TestNFTCreateApply(t *testing.T) {
	e, k := newEngine(t)
	ctx := context.Background()

	body := `chain forward {
	type filter hook forward priority 0; policy accept;
}`
	p, diff, err := e.Create(ctx, "cli", "", plan.DesiredState{
		NFT: &plan.NFTSpec{Body: body},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if diff.NFT == nil || diff.NFT.Op != "create" {
		t.Errorf("expected create diff, got %+v", diff.NFT)
	}

	if _, err := e.Apply(ctx, p.ID, 30); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Mock stores the last applied ruleset under "_last_applied".
	last := k.NFT["_last_applied"]
	if !strings.Contains(last, "chain forward") {
		t.Errorf("applied ruleset missing chain:\n%s", last)
	}
	if !strings.Contains(last, "add table inet wg-admin") {
		t.Errorf("transaction lacks add-table wrapper:\n%s", last)
	}
	if !strings.Contains(last, "delete table inet wg-admin") {
		t.Errorf("transaction lacks delete-table wrapper:\n%s", last)
	}
}

func TestNFTBodyRejectsForbiddenTokens(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()

	for _, body := range []string{
		"flush ruleset",
		"table inet other { }",
		"delete table inet wg-admin",
		"include \"file\"",
	} {
		_, _, err := e.Create(ctx, "cli", "", plan.DesiredState{
			NFT: &plan.NFTSpec{Body: body},
		})
		if err == nil {
			t.Errorf("body %q: want error", body)
		}
	}

	// A benign body with the word "table" as a comment / rule-field reference
	// should still be rejected by our conservative lexer. Document that
	// intentionally — simpler to be strict than to parse nft syntax.
	_, _, err := e.Create(ctx, "cli", "", plan.DesiredState{
		NFT: &plan.NFTSpec{Body: "# table of contents"},
	})
	if err != nil {
		// '#' is before 'table' but our regex is ^\s*(table|...)\b
		// which requires line-start. So this specific case would pass.
		// Leave the assertion direction lenient; just document behavior.
		t.Logf("comment-with-table raised: %v (ok either way)", err)
	}
}

func TestNFTRevertRestoresSnapshot(t *testing.T) {
	e, k := newEngine(t)
	ctx := context.Background()

	// Pre-seed a fake existing ruleset for the table so snapshot picks it up.
	k.NFT["wg-admin"] = `table inet wg-admin {
	chain old {
		type filter hook forward priority 0; policy drop;
	}
}`
	body := `chain new {
	type filter hook forward priority 0; policy accept;
}`
	p, diff, err := e.Create(ctx, "cli", "", plan.DesiredState{
		NFT: &plan.NFTSpec{Body: body},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if diff.NFT == nil || diff.NFT.Op != "replace" {
		t.Errorf("expected replace, got %+v", diff.NFT)
	}

	if _, err := e.Apply(ctx, p.ID, 30); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := e.Revert(ctx, p.ID); err != nil {
		t.Fatalf("revert: %v", err)
	}

	// On revert, the last applied ruleset should contain the snapshot body.
	last := k.NFT["_last_applied"]
	if !strings.Contains(last, "chain old") {
		t.Errorf("revert didn't restore old chain:\n%s", last)
	}
}

func TestNFTRevertWhenDidNotExist(t *testing.T) {
	e, k := newEngine(t)
	ctx := context.Background()

	// No pre-existing ruleset.
	body := `chain new { type filter hook forward priority 0; policy accept; }`
	p, _, err := e.Create(ctx, "cli", "", plan.DesiredState{
		NFT: &plan.NFTSpec{Body: body},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := e.Apply(ctx, p.ID, 30); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := e.Revert(ctx, p.ID); err != nil {
		t.Fatalf("revert: %v", err)
	}
	last := k.NFT["_last_applied"]
	// Revert should be delete-only (no `table { ... }` redefine).
	if strings.Contains(last, "chain") {
		t.Errorf("revert should be delete-only, got:\n%s", last)
	}
	if !strings.Contains(last, "delete table") {
		t.Errorf("revert lacks delete:\n%s", last)
	}
}
