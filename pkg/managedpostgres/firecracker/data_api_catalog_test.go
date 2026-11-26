package firecracker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestDataAPICatalogPreviewUsesReadOnlyIdentityAndRedactsConnectionErrors(t *testing.T) {
	p, scheduler, verifiers := newTestProvider(t)
	if _, err := p.Provision(context.Background(), managedpostgres.ProvisionRequest{ResourceID: testDatabaseID, Spec: devSpec()}); err != nil {
		t.Fatal(err)
	}
	var observed managedpostgres.CredentialMaterial
	p.WithConnector(func(_ context.Context, material managedpostgres.CredentialMaterial) (*pgx.Conn, error) {
		observed = material
		return nil, errors.New("secret connection string")
	})
	_, err := p.PreviewDataAPICatalog(context.Background(), testDatabaseID)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error=%v", err)
	}
	if observed.Username == "" || observed.Password == "" || len(scheduler.admin) == 0 {
		t.Fatal("read-only identity was not issued")
	}
	if !strings.Contains(scheduler.admin[len(scheduler.admin)-1], "GRANT SELECT ON ALL TABLES IN SCHEMA public") {
		t.Fatal("preview did not request read-only privileges")
	}
	if len(verifiers.byID[testDatabaseID]) == 0 {
		t.Fatal("preview verifier was not published")
	}
	if _, ok := any(p).(managedpostgres.ReadOnlyQuerier); ok {
		t.Fatal("catalog preview enabled arbitrary SQL queries")
	}
}
