package core

import "testing"

func TestCreateFingerprintCanonicalDefaultsAndTags(t *testing.T) {
	base := CreateIssueRequest{
		Project: "afc", ScopeKind: "project", Title: "one", Actor: "agent",
		Tags: []string{"area/test", "risk/low"},
	}
	canonical := base
	canonical.IssueType = "task"
	canonical.Priority = 3
	canonical.Tags = []string{"risk/low", "area/test"}
	fingerprint := func(r CreateIssueRequest) string {
		return OperationFingerprint(CreateFingerprintFields("afc", r))
	}
	if fingerprint(base) != fingerprint(canonical) {
		t.Fatal("equivalent default and tag order produced different fingerprints")
	}
	changes := []struct {
		name   string
		change func(*CreateIssueRequest)
	}{
		{"actor", func(r *CreateIssueRequest) { r.Actor = "other" }},
		{"title", func(r *CreateIssueRequest) { r.Title = "other" }},
		{"repo", func(r *CreateIssueRequest) { r.Repo = "other" }},
		{"tags", func(r *CreateIssueRequest) { r.Tags = []string{"area/test"} }},
	}
	for _, check := range changes {
		t.Run(check.name, func(t *testing.T) {
			changed := base
			check.change(&changed)
			if fingerprint(changed) == fingerprint(base) {
				t.Fatal("changed request reused fingerprint")
			}
		})
	}
}
