package main_test

import (
	"errors"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestPerInstanceConcurrencyRefusalIsExplained pins that the dark-launch
// refusal reads as "not enabled for this account", not an unknown field.
func TestPerInstanceConcurrencyRefusalIsExplained(t *testing.T) {
	refusal := errors.New("Invalid scaling policy: unknown field(s): per_instance_concurrency")
	if got := main.ExplainPerInstanceConcurrencyRefusal(refusal, true); !strings.Contains(got.Error(), "not enabled for this account") || !errors.Is(got, refusal) {
		t.Fatalf("explained = %v", got)
	}
	if got := main.ExplainPerInstanceConcurrencyRefusal(refusal, false); !errors.Is(got, refusal) || got.Error() != refusal.Error() {
		t.Fatalf("a request without the flag was rewritten: %v", got)
	}
	other := errors.New("plan limit")
	if got := main.ExplainPerInstanceConcurrencyRefusal(other, true); !errors.Is(got, other) || got.Error() != other.Error() {
		t.Fatalf("another error was rewritten: %v", got)
	}
}

// prod 2026-11-19: printErr renders the APIError in the chain, so the wrapped
// explanation never reached the screen. The problem itself carries it now.
func TestPerInstanceConcurrencyRefusalRewritesTheProblem(t *testing.T) {
	refusal := &main.APIError{Problem: api.Problem{Code: "invalid_request", Status: 400, Detail: "unknown field(s): per_instance_concurrency"}}
	got := main.ExplainPerInstanceConcurrencyRefusal(refusal, true)
	var ae *main.APIError
	if !errors.As(got, &ae) || !strings.Contains(ae.Problem.Detail, "early access") || ae.Problem.Hint == "" || ae.Problem.Status != 400 {
		t.Fatalf("explained = %#v", got)
	}
}
