// Tests for the Providers() registry + the CapabilitySet shape.
// Companion to loader_test.go (which covers the FAAS_BILLING_PROVIDER
// selector). The capability surface is the new contract added in
// PR-P1 of the pluggable-billing rollout; these tests pin both the
// reported bitmask per provider and the registry metadata.
package loader

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/billing"
)

func TestProviders_RegistersAllProviders(t *testing.T) {
	// PR-P2: alphabetical sort (paddle, polar, stripe). The PR-P1 slice
	// literal was [stripe, paddle]; after PR-P2 the registry order
	// is determined by Register() call order at init() time, which
	// is implementation-defined. The loader sorts by Name to keep
	// output deterministic, so this test asserts the alphabetical
	// ordering.
	got := Providers()
	want := []string{"paddle", "polar", "stripe"}
	if len(got) != len(want) {
		t.Fatalf("Providers() returned %d entries, want %d (%v)", len(got), len(want), got)
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("Providers()[%d].Name = %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestProviders_Stripe(t *testing.T) {
	for _, m := range Providers() {
		if m.Name != "stripe" {
			continue
		}
		// Stripe exposes: refund, metered usage, sandbox.
		// No hosted checkout (returns ("", "", nil) — apid falls
		// through to FAAS_BILLING_PORTAL_URL).
		// No usage_reconcile (returns ErrNotImplemented).
		// No usage_line_item (pushes metered records).
		if !m.Capabilities.Has(billing.CapRefund) {
			t.Error("stripe missing CapRefund")
		}
		if !m.Capabilities.Has(billing.CapUsageMetered) {
			t.Error("stripe missing CapUsageMetered")
		}
		if !m.Capabilities.Has(billing.CapSandbox) {
			t.Error("stripe missing CapSandbox")
		}
		if m.Capabilities.Has(billing.CapHostedCheckout) {
			t.Error("stripe should NOT include CapHostedCheckout — falls back to portal URL")
		}
		if m.Capabilities.Has(billing.CapUsageReconcile) {
			t.Error("stripe should NOT include CapUsageReconcile — returns ErrNotImplemented")
		}
		if m.Capabilities.Has(billing.CapUsageLineItem) {
			t.Error("stripe should NOT include CapUsageLineItem — pushes metered records")
		}
		return
	}
	t.Fatal("stripe provider not found in Providers()")
}

func TestProviders_Paddle(t *testing.T) {
	for _, m := range Providers() {
		if m.Name != "paddle" {
			continue
		}
		// Paddle exposes: hosted checkout, refunds, line-item usage, sandbox.
		// No usage_reconcile (Paddle Billing has no usage-summary endpoint).
		if !m.Capabilities.Has(billing.CapHostedCheckout) {
			t.Error("paddle missing CapHostedCheckout")
		}
		if !m.Capabilities.Has(billing.CapUsageLineItem) {
			t.Error("paddle missing CapUsageLineItem")
		}
		if !m.Capabilities.Has(billing.CapSandbox) {
			t.Error("paddle missing CapSandbox")
		}
		if !m.Capabilities.Has(billing.CapRefund) {
			t.Error("paddle missing CapRefund")
		}
		if m.Capabilities.Has(billing.CapUsageReconcile) {
			t.Error("paddle should NOT include CapUsageReconcile — Paddle has no usage-summary endpoint")
		}
		if m.Capabilities.Has(billing.CapUsageMetered) {
			t.Error("paddle should NOT include CapUsageMetered — pushes line items")
		}
		return
	}
	t.Fatal("paddle provider not found in Providers()")
}

func TestProviders_Polar(t *testing.T) {
	for _, m := range Providers() {
		if m.Name != "polar" {
			continue
		}
		if !m.Capabilities.Has(billing.CapHostedCheckout) {
			t.Error("polar missing CapHostedCheckout")
		}
		if !m.Capabilities.Has(billing.CapRefund) {
			t.Error("polar missing CapRefund")
		}
		if !m.Capabilities.Has(billing.CapUsageMetered) {
			t.Error("polar missing CapUsageMetered")
		}
		if !m.Capabilities.Has(billing.CapSandbox) {
			t.Error("polar missing CapSandbox")
		}
		if m.Capabilities.Has(billing.CapUsageReconcile) {
			t.Error("polar should not advertise CapUsageReconcile")
		}
		return
	}
	t.Fatal("polar provider not found in Providers()")
}

func TestProviders_HasEnvVarsPerProvider(t *testing.T) {
	for _, m := range Providers() {
		if len(m.EnvVars) == 0 {
			t.Errorf("provider %q has no env vars listed", m.Name)
		}
	}
}

func TestCapabilitySet_String(t *testing.T) {
	// Empty set reports "none".
	if (billing.CapabilitySet(0)).String() != "none" {
		t.Errorf("empty CapabilitySet.String() = %q, want %q", billing.CapabilitySet(0).String(), "none")
	}
	// Single-bit set.
	got := billing.CapabilitySet(billing.CapHostedCheckout).String()
	if got != "hosted_checkout" {
		t.Errorf("single-bit String() = %q, want %q", got, "hosted_checkout")
	}
	// Multi-bit set — order matches iota declaration in
	// pkg/billing/provider.go (CapHostedCheckout, CapRefund,
	// CapUsageReconcile, CapSandbox, CapUsageMetered, CapUsageLineItem),
	// filtered to the requested bits.
	combined := billing.CapabilitySet(billing.CapHostedCheckout | billing.CapUsageLineItem | billing.CapSandbox)
	if got := combined.String(); got != "hosted_checkout,sandbox,usage_line_item" {
		t.Errorf("multi-bit String() = %q, want canonical iota order", got)
	}
}

func TestCapabilitySet_Has(t *testing.T) {
	combined := billing.CapabilitySet(billing.CapRefund | billing.CapUsageMetered)
	if !combined.Has(billing.CapRefund) {
		t.Error("Has(CapRefund) = false, want true")
	}
	if !combined.Has(billing.CapUsageMetered) {
		t.Error("Has(CapUsageMetered) = false, want true")
	}
	if combined.Has(billing.CapHostedCheckout) {
		t.Error("Has(CapHostedCheckout) = true, want false")
	}
}
