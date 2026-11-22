package sched_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
)

// Prod hunt r27: a thrown Python handler's invocation read "invoke: sched:
// permanent invoke error: application returned HTTP 500: …".
func TestInvocationErrorTextDropsTheInternalClassification(t *testing.T) {
	err := fmt.Errorf("%w: application returned HTTP %d: %s", sched.ErrPermanentInvoke, 500, `{"error":"handler_error"}`)
	if got, want := sched.InvocationErrorText(err), `application returned HTTP 500: {"error":"handler_error"}`; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	if !errors.Is(err, sched.ErrPermanentInvoke) {
		t.Fatal("the sentinel must stay in the chain")
	}
	if got := sched.InvocationErrorText(sched.ErrDispatchTimeout); got != sched.ErrDispatchTimeout.Error() {
		t.Fatalf("other errors are unchanged: %q", got)
	}
	if sched.InvocationErrorText(nil) != "" {
		t.Fatal("nil error")
	}
}
