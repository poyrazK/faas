// adr: 93

package apidserver_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// Hunt #9 (production-us): under apid's 5 s request budget a project apply
// committed its apps, then failed every build enqueue on the deadline and
// left the apps without a deployment.
func TestProjectApplyContextOutlivesRequestBudget(t *testing.T) {
	parent, cancelParent, _ := reqbudget.WithRemaining(context.Background(),
		api.RequestBudgetApidDefault, api.RequestBudgetMax, "admin", "POST:/v1/projects")
	ctx, cancel := apidserver.ProjectApplyContext(parent)
	defer cancel()
	cancelParent()

	if err := ctx.Err(); err != nil {
		t.Fatalf("apply context ended with the request budget: %v", err)
	}
	if _, ok := reqbudget.FromContext(ctx); ok {
		t.Fatal("apply context still carries the request budget")
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("apply context has no deadline")
	}
	if remaining := time.Until(deadline); remaining < api.ProjectApplyTimeout-time.Minute || remaining > api.ProjectApplyTimeout {
		t.Fatalf("apply deadline in %v, want about %v", remaining, api.ProjectApplyTimeout)
	}
}
