package monetization

import (
	"slices"
	"testing"
)

// adr: 20261018090210 — the gate covers every monetization path and its
// children, survives path tricks, and leaves neighbouring routes alone.
func TestPath(t *testing.T) {
	for _, p := range []string{
		"/v1/apps/shop/rate-cards",
		"/v1/apps/shop/rate-cards/rc_1",
		"/v1/apps/{slug}/rate-cards",
		"/v1/apps/shop/consumer-plans/plan_1",
		"/v1/apps/shop/consumers/c_1/usage/quote",
		"/v1/apps/shop/consumers/c_1/usage-statements/s_1/invoice",
		"/v1/apps/shop/consumers/c_1/credits/cr_1",
		"/v1/apps/shop/consumers/c_1/trial",
		"/v1/account/billing-runs/run_1/items",
		"/v1/account/billing-connections/stripe",
		"/v1/account/statement-invoices",
		"/v1/account/platform-tenant-plans/plan_1",
		"/v1/account/platform-tenants/t_1/usage-statements/s_1/finalize",
		"/v1/account/platform-tenants/t_1/trial",
		"/v1/consumer-billing/shop/statements",
		"/v1/consumer-signup/shop/page",
		"/v1/apps/shop/consumer-signup",
		"/v1/apps/shop/consumer-signups",
		"/v1/platform-tenant-self/usage-statements",
		"/v1/apps/shop/./rate-cards",
		"/v1/apps/shop/x/../rate-cards",
		"//v1/apps/shop//rate-cards/",
	} {
		if !Path(p) {
			t.Errorf("%s is not gated", p)
		}
	}
	for _, p := range []string{
		"/v1/apps/shop",
		"/v1/apps/shop/consumers",
		"/v1/apps/shop/consumers/c_1",
		"/v1/apps/shop/consumers/c_1/usage",
		"/v1/apps/shop/consumers/c_1/keys",
		"/v1/apps/shop/rate-cards-archive",
		"/v1/apps//rate-cards",
		"/v1/account/platform-tenants",
		"/v1/account/platform-tenants/t_1/usage",
		"/v1/account/platform-tenants/t_1/webhooks",
		"/v1/account/platform-tenants/t_1/request-budget",
		"/v1/account/billing",
		"/v1/billing/status",
		"/v1/invoices",
		"/v1/platform-tenant-self/usage",
	} {
		if Path(p) {
			t.Errorf("%s is gated", p)
		}
	}
}

func TestVisibleWebhookEvents(t *testing.T) {
	all := []string{"deployment.live", "usage_statement.finalized", "consumer.usage_threshold", "job.finished"}
	if got := VisibleWebhookEvents(all, false); !slices.Equal(got, []string{"deployment.live", "job.finished"}) {
		t.Errorf("hidden = %v", got)
	}
	if got := VisibleWebhookEvents(all, true); !slices.Equal(got, all) {
		t.Errorf("released = %v", got)
	}
}
