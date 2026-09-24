package main

import (
	"os"
	"strings"
	"testing"
)

func TestEmbeddedProtocolMatchesCanonical(t *testing.T) {
	canonical, err := os.ReadFile("../../docs/agent-protocol-v1.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != protocolDoc {
		t.Error("embedded cmd/dibs/agent-protocol-v1.md differs from canonical docs/agent-protocol-v1.md")
		t.Logf("canonical: %d bytes", len(canonical))
		t.Logf("embedded:  %d bytes", len(protocolDoc))
	}
}

func TestEmbeddedProtocolPublishesRetryAndTokenRules(t *testing.T) {
	for _, phrase := range []string{
		"Agents never pass", "DIBS_LEASE_TOKEN_FILE", "original, historical",
		"new** operation ID", "Timeout at or after", "external consumer",
	} {
		if !strings.Contains(protocolDoc, phrase) {
			t.Errorf("dibs protocol lacks %q", phrase)
		}
	}
}
