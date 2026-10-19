package apidserver_test

import (
	"os"
	"testing"
)

// TestMain releases API monetization (ADR-20261018090210) for the test
// binary, internal and external tests alike, which exercise it;
// api_monetization_test.go turns it off per test.
func TestMain(m *testing.M) {
	_ = os.Setenv("FAAS_API_MONETIZATION_ENABLED", "1")
	os.Exit(m.Run())
}
