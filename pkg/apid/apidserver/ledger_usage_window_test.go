package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// ChronoLake finding 32: without until, consumer and platform-tenant usage
// ended the window at the start of today UTC and hid every same-day request.
func TestLedgerUsageDefaultWindowIncludesToday(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "ledger-today")
	create := e.Do(t, http.MethodPost, "/v1/account/platform-tenants", api.CreatePlatformTenantRequest{ExternalRef: "ledger-today", Name: "Ledger Today"}, nil)
	var tenant api.PlatformTenantResponse
	if create.Code != http.StatusCreated || json.Unmarshal(create.Body.Bytes(), &tenant) != nil {
		t.Fatalf("create tenant: %d %s", create.Code, create.Body.String())
	}
	consumer := seedPlatformTenantConsumer(t, e, "ledger-today", tenant.ID)
	now := time.Now().UTC()
	if _, err := (*e.Store()).RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: (*e.Acct()).ID, AppID: appID, ConsumerKey: consumer,
		PlatformTenantID: tenant.ID, WindowStart: now.Truncate(time.Minute), RequestCount: 3, BillableUnits: 3,
	}); err != nil {
		t.Fatal(err)
	}
	nextMidnight := now.Truncate(24*time.Hour).AddDate(0, 0, 1)

	res := e.Do(t, http.MethodGet, "/v1/account/platform-tenants/"+tenant.ID+"/usage", nil, nil)
	var tenantUsage api.PlatformTenantUsageResponse
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &tenantUsage) != nil {
		t.Fatalf("tenant usage: %d %s", res.Code, res.Body.String())
	}
	if tenantUsage.RequestCount != 3 || !tenantUsage.PeriodEnd.Equal(nextMidnight) {
		t.Fatalf("tenant usage hid today: %s", res.Body.String())
	}

	res = e.Do(t, http.MethodGet, "/v1/apps/ledger-today/consumers/"+consumer+"/usage", nil, nil)
	var consumerUsage api.APIConsumerUsageResponse
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &consumerUsage) != nil {
		t.Fatalf("consumer usage: %d %s", res.Code, res.Body.String())
	}
	if consumerUsage.RequestCount != 3 || !consumerUsage.PeriodEnd.Equal(nextMidnight) {
		t.Fatalf("consumer usage hid today: %s", res.Body.String())
	}
}
