package main_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestAddPostgresCreateKeyCoversTheRequest pins that a create retried with a
// corrected --region (or any other setting) uses a new idempotency key, so the
// API does not refuse it as a reused key for 24 hours.
func TestAddPostgresCreateKeyCoversTheRequest(t *testing.T) {
	base := api.CreateManagedPostgresDatabaseRequest{Name: "orders", Region: "us-east-1"}
	moved := base
	moved.Region = "us-east-2"
	same := main.AddPostgresCreateKeyParts("api", "default", "DATABASE_URL", base)
	if !slices.Equal(same, main.AddPostgresCreateKeyParts("api", "default", "DATABASE_URL", base)) {
		t.Fatal("an identical retry changed its key")
	}
	if slices.Equal(same, main.AddPostgresCreateKeyParts("api", "default", "DATABASE_URL", moved)) {
		t.Fatal("a retry with another --region kept the key")
	}
}

// TestPerInstanceConcurrencyRefusalIsExplained pins that the dark-launch
// refusal reads as "not enabled for this account", not an unknown field.
func TestPerInstanceConcurrencyRefusalIsExplained(t *testing.T) {
	refusal := errors.New("Invalid scaling policy: unknown field(s): per_instance_concurrency")
	if got := main.ExplainPerInstanceConcurrencyRefusal(refusal, true); !strings.Contains(got.Error(), "not enabled for this account") || !errors.Is(got, refusal) {
		t.Fatalf("explained = %v", got)
	}
	if got := main.ExplainPerInstanceConcurrencyRefusal(refusal, false); got != refusal {
		t.Fatalf("a request without the flag was rewritten: %v", got)
	}
	other := errors.New("plan limit")
	if got := main.ExplainPerInstanceConcurrencyRefusal(other, true); got != other {
		t.Fatalf("another error was rewritten: %v", got)
	}
}
