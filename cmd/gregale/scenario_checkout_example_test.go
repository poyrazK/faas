package main_test

import (
	"context"
	"github.com/onebox-faas/faas/cmd/gregale"
	"os/exec"
	"testing"
	"time"
)

func TestCheckoutResilienceExampleManifest(t *testing.T) {
	manifest, dir, err := main.ReadTestManifestDocument("../../examples/scenario-checkout/gregale-test.yaml", func(string, main.TestScenario) []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := main.CollectTestValidation(manifest.Scenarios, dir, "checkout-outage"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckoutConcurrentExampleManifest(t *testing.T) {
	manifest, dir, err := main.ReadTestManifestDocument("../../examples/scenario-checkout/gregale-batch-test.yaml", func(string, main.TestScenario) []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := main.CollectTestValidation(manifest.Scenarios, dir, "checkout-batch"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckoutResilienceExampleAcceptance(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is needed for checkout example acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--test", "../../examples/scenario-checkout/test/checkout.test.mjs", "../../examples/scenario-checkout/test/durable.test.mjs", "../../examples/scenario-checkout/test/batch.test.mjs", "../../examples/scenario-checkout/test/native-probe.test.mjs")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout acceptance: %v\n%s", err, output)
	}
}
