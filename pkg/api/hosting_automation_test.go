package api

import (
	"bytes"
	"strings"
	"testing"
)

func TestHostingAutomationTokensHaveSeparateNamespaceAndHash(t *testing.T) {
	// adr: 202610170925 — automation tokens never authenticate as account keys.
	first, hash, err := GenerateHostingAutomationToken()
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := GenerateHostingAutomationToken()
	if err != nil || first == second || !ValidHostingAutomationToken(first) || ValidAPIKeyFormat(first) || !bytes.Equal(hash, HashAPIKey(first)) {
		t.Fatal("invalid token generation or namespace", err)
	}
	for _, invalid := range []string{"", HostingAutomationTokenPrefix, HostingAutomationTokenPrefix + strings.Repeat("A", 64), HostingAutomationTokenPrefix + strings.Repeat("a", 63), "fp_live_" + strings.Repeat("a", 64), first + "\n"} {
		if ValidHostingAutomationToken(invalid) {
			t.Fatal("invalid token accepted")
		}
	}
}
