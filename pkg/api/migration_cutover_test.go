package api_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMigrationCutoverRejectsCredentialsAndInvalidRecovery(t *testing.T) {
	good := api.CreateMigrationCutoverRequest{ID: uuid.NewString(), VerificationID: uuid.NewString(), Hostname: "api.example.test", PreviousRecords: []api.MigrationDNSRecord{{Type: "CNAME", Value: "old.example.test", TTL: 300}}, Max5xxRateBPS: 100, MaxP95MS: 1000}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	qualified := good
	qualified.PreviousRecords = []api.MigrationDNSRecord{{Type: "CNAME", Value: "Old.Provider.Test.", TTL: 0}}
	if err := qualified.Validate(); err != nil {
		t.Fatalf("valid original DNS spelling/TTL rejected: %v", err)
	}
	for _, value := range []string{"https://secret@provider.test/", "provider.test/path?token=secret", "*.example.test", "127.0.0.1", "provider.test:443"} {
		in := good
		in.PreviousRecords = []api.MigrationDNSRecord{{Type: "CNAME", Value: value, TTL: 300}}
		if err := in.Validate(); err == nil {
			t.Errorf("accepted record %q", value)
		}
		in = good
		in.Hostname = value
		if err := in.Validate(); err == nil {
			t.Errorf("accepted hostname %q", value)
		}
	}
	for _, records := range [][]api.MigrationDNSRecord{nil, {{Type: "TXT", Value: "secret", TTL: 300}}, {{Type: "CNAME", Value: "old.example.test", TTL: -1}}, {{Type: "CNAME", Value: "old.example.test", TTL: 300}, {Type: "A", Value: "8.8.8.8", TTL: 300}}} {
		in := good
		in.PreviousRecords = records
		if err := in.Validate(); err == nil {
			t.Errorf("accepted invalid recovery: %+v", records)
		}
	}
}
