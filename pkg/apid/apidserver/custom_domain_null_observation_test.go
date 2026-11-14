// adr: 520
package apidserver_test

import (
	"context"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomDomainShowCannotUndoConcurrentDNSRevocationWithoutOldTimestamp(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "false")
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "legacy-clock-revocation")
	const host = "legacy-clock.example.com"
	domainRecoveryVerified(t, store, host, app, "proof")
	configureDomainDNS(t)
	t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", "")
	*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return "edge.gregale.dev.", nil }
	*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) { return []string{"8.8.8.8"}, nil }
	*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) { return nil, nil }
	at := time.Now().UTC()
	*apidserver.DialCertFunc = func(ctx context.Context, _ string) (*x509.Certificate, error) {
		changed, err := store.MarkCustomDomainDNSDrifted(ctx, host, at, "new wrong CNAME")
		if err != nil || !changed {
			return nil, fmt.Errorf("concurrent revocation: changed=%v err=%w", changed, err)
		}
		return &x509.Certificate{NotAfter: at.Add(24 * time.Hour), DNSNames: []string{host}}, nil
	}
	response := e.Do(t, http.MethodGet, "/v1/domains/"+host, nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("show: %d %s", response.Code, response.Body)
	}
	current, err := store.DomainByName(t.Context(), host)
	if err != nil || current.Verified() || current.CertStatus != state.CustomDomainCertDNSDrifted || !current.DNSLastCheckedAt.Equal(at) {
		t.Fatalf("show overwrote DNS revocation: %+v %v", current, err)
	}
	oldTXT := *apidserver.TxtLookupFunc
	t.Cleanup(func() { *apidserver.TxtLookupFunc = oldTXT })
	*apidserver.TxtLookupFunc = func(context.Context, string) ([]string, error) { return []string{"proof"}, nil }
	*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return "wrong-provider.example.net.", nil }
	*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) { return []string{"1.1.1.1"}, nil }
	(*e.S()).RunVerifyOnce(t.Context(), slog.New(slog.DiscardHandler))
	current, err = store.DomainByName(t.Context(), host)
	if err != nil || current.Verified() || current.CertStatus != state.CustomDomainCertDNSDrifted {
		t.Fatalf("TXT proof restored a wrong CNAME: %+v %v", current, err)
	}
}
