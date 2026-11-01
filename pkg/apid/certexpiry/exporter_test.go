package certexpiry_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/apid/certexpiry"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeStore struct {
	expiries []state.CustomDomainCertExpiry
	err      error
	reads    int
	limit    int
}

func (f *fakeStore) SoonestCustomDomainCertExpiries(_ context.Context, limit int) ([]state.CustomDomainCertExpiry, error) {
	f.reads++
	f.limit = limit
	return f.expiries, f.err
}

var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// The FaasTLSCertExpiry* alerts read these exact names; nothing in production
// set them before (bug hunt 32 M6).
func TestExporterPublishesTheAlertMetrics(t *testing.T) {
	store := &fakeStore{expiries: []state.CustomDomainCertExpiry{
		{Domain: "api.shop.example", NotAfter: now.Add(2 * 24 * time.Hour)},
		{Domain: "*.acme.example", NotAfter: now.Add(20 * 24 * time.Hour)},
	}}
	e := certexpiry.New(store, func() time.Time { return now }, nil)
	want := `
# HELP gateway_tls_cert_expiry_by_host_seconds Remaining lifetime of the edge certificate the domain doctor observed, for the soonest-expiring verified custom domains (at most CustomDomainCertExpiryExportMaxHosts). kind is ondemand: the edge obtains every customer certificate on demand.
# TYPE gateway_tls_cert_expiry_by_host_seconds gauge
gateway_tls_cert_expiry_by_host_seconds{hostname="*.acme.example",kind="ondemand"} 1.728e+06
gateway_tls_cert_expiry_by_host_seconds{hostname="api.shop.example",kind="ondemand"} 172800
# HELP gateway_tls_cert_expiry_seconds Smallest remaining lifetime of the edge certificates apid's domain doctor observed on verified custom domains (custom_domains.cert_expires_at). Absent until a certificate is observed. Negative means a certificate is already past its NotAfter.
# TYPE gateway_tls_cert_expiry_seconds gauge
gateway_tls_cert_expiry_seconds 172800
`
	if err := testutil.CollectAndCompare(e, strings.NewReader(want)); err != nil {
		t.Fatal(err)
	}
	if store.limit <= 0 {
		t.Fatalf("read limit = %d, want the bounded host cap", store.limit)
	}
}

func TestExporterExportsNothingWithoutData(t *testing.T) {
	for name, store := range map[string]*fakeStore{
		"no observed certificate": {},
		"store error":             {err: errors.New("database unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			e := certexpiry.New(store, func() time.Time { return now }, nil)
			if n := testutil.CollectAndCount(e); n != 0 {
				t.Fatalf("exported %d series, want none", n)
			}
		})
	}
}

func TestExporterCachesOneReadAcrossScrapes(t *testing.T) {
	store := &fakeStore{expiries: []state.CustomDomainCertExpiry{{Domain: "a.example", NotAfter: now.Add(time.Hour)}}}
	clock := now
	e := certexpiry.New(store, func() time.Time { return clock }, nil)
	reg := prometheus.NewRegistry()
	reg.MustRegister(e)
	for i := 0; i < 3; i++ {
		if _, err := reg.Gather(); err != nil {
			t.Fatal(err)
		}
	}
	if store.reads != 1 {
		t.Fatalf("store reads = %d across three scrapes, want 1", store.reads)
	}
	clock = clock.Add(2 * time.Minute)
	if _, err := reg.Gather(); err != nil {
		t.Fatal(err)
	}
	if store.reads != 2 {
		t.Fatalf("store reads = %d after the cache window, want 2", store.reads)
	}
}
