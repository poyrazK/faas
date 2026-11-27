package apidserver

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/apid/capgate"
	"github.com/onebox-faas/faas/pkg/dbstorage"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/productcap"
)

type storageAlertMailer struct {
	sent int
}

func (m *storageAlertMailer) Send(context.Context, Message) error {
	m.sent++
	return nil
}

func TestPostgresStorageAlertsRespectCapability(t *testing.T) {
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	key, ok := platformflags.CapabilityFlag(nativePostgresOperationsCapability)
	if !ok {
		t.Fatal("native PostgreSQL operations must have a platform flag")
	}
	org := uuid.NewString()
	allow := true
	flags := platformflags.NewSet(map[string]platformflags.Definition{
		key: {Enabled: true, Rules: []platformflags.Rule{{ID: "native-preview", Orgs: []string{org}, Value: &allow}}},
	})
	mailer := &storageAlertMailer{}
	s := &server{mailer: mailer, capGate: capgate.New(catalog, flags.Snapshot)}
	alert := dbstorage.Alert{ID: uuid.NewString(), State: "low_space", DatabaseName: "orders"}
	recipient := dbstorage.AlertRecipient{AccountID: uuid.NewString(), OrgID: uuid.NewString(), Plan: "pro", Email: "owner@example.test"}
	if err := s.sendManagedPostgresStorageAlert(t.Context(), alert, recipient); err != nil || mailer.sent != 0 {
		t.Fatalf("disabled delivery sent %d messages, error %v", mailer.sent, err)
	}
	recipient.OrgID = org
	if err := s.sendManagedPostgresStorageAlert(t.Context(), alert, recipient); err != nil || mailer.sent != 1 {
		t.Fatalf("enabled delivery sent %d messages, error %v", mailer.sent, err)
	}
}
