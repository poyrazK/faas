package meter

import (
	"context"
	"testing"
)

func TestDisabledBillingTickIsHealthyWithoutProvider(t *testing.T) {
	l := &Loop{billingDisabled: true, cfg: &Config{}}
	p := NewPusher(nil, nil, nil, nil, nil)
	if err := l.pushBillingOnce(context.Background(), p); err != nil {
		t.Fatalf("disabled tick = %v, want nil", err)
	}
	l.billingDisabled = false
	if err := l.pushBillingOnce(context.Background(), p); err == nil {
		t.Fatal("live tick unexpectedly accepted a missing provider")
	}
}
