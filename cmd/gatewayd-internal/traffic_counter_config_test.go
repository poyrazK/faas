// adr: 570
package main

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestTrafficCountersRequirePoolByDefault(t *testing.T) {
	t.Setenv("FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL", "")
	t.Setenv("FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL_FILE", "")
	for _, cfg := range []*Config{nil, {RateLimit: TOMLRateLimitConfig{Mode: "central"}}} {
		_, err := buildTrafficRetryBudget(context.Background(), cfg, nil, gateway.NewMetrics(), discardLogger())
		if err == nil || !strings.Contains(err.Error(), "Postgres pool") {
			t.Fatalf("default requires shared counters: %v", err)
		}
	}
	cfg := &Config{RateLimit: TOMLRateLimitConfig{Mode: "local"}}
	b, err := buildTrafficRetryBudget(t.Context(), cfg, nil, gateway.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.BackendID() != "" {
		t.Fatal("explicit local mode claimed a shared endpoint")
	}
}
