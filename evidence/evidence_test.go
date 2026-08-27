package evidence

import "testing"

func TestValidateLinksRejectsUnknownReference(t *testing.T) {
	summary := Summary{EvidenceLines: []Line{{ID: "E1", Text: "failed"}}}
	result := ValidateLinks(summary, "because [E2]", true)
	if result.Valid {
		t.Fatal("expected invalid result")
	}
}

func TestValidateLinksRequiresReference(t *testing.T) {
	summary := Summary{EvidenceLines: []Line{{ID: "E1", Text: "failed"}}}
	result := ValidateLinks(summary, "because it failed", true)
	if result.Valid {
		t.Fatal("expected missing-reference invalid result")
	}
}
