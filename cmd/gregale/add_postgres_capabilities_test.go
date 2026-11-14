package main_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type addPostgresCapabilitiesStub struct {
	capabilities api.ManagedPostgresCapabilities
	err          error
}

func (s addPostgresCapabilitiesStub) GetManagedPostgresCapabilities(context.Context, string) (api.ManagedPostgresCapabilities, error) {
	return s.capabilities, s.err
}

// Production us-east-2 offered migration/read_only/read_write and single_zone
// while the CLI accepted data_api and high_availability.
func TestAddPostgresUnsupportedChecksRegionCapabilities(t *testing.T) {
	production := addPostgresCapabilitiesStub{capabilities: api.ManagedPostgresCapabilities{
		CredentialAccess: []string{"migration", "read_only", "read_write"}, Availability: []string{"single_zone"}}}
	for _, tc := range []struct {
		name, access, availability, want string
		creating                         bool
		stub                             addPostgresCapabilitiesStub
	}{
		{name: "supported", access: "read_write", availability: "single_zone", creating: true, stub: production},
		{name: "data api unsupported", access: "data_api", availability: "single_zone", creating: true, stub: production, want: "--access data_api"},
		{name: "high availability unsupported", access: "read_write", availability: "high_availability", creating: true, stub: production, want: "--availability high_availability"},
		{name: "availability ignored when attaching", access: "read_write", availability: "high_availability", stub: production},
		{name: "unreadable capabilities never block", access: "data_api", availability: "high_availability", creating: true, stub: addPostgresCapabilitiesStub{err: errors.New("offline")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := main.AddPostgresUnsupported(context.Background(), tc.stub, "us-east-2", tc.access, tc.availability, tc.creating)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected refusal: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "available:") {
				t.Fatalf("err = %v, want mention of %q and the available choices", err, tc.want)
			}
		})
	}
}

func TestAddPostgresMigrationHintOnlyForRuntimeBindings(t *testing.T) {
	var out bytes.Buffer
	main.PrintAddPostgresMigrationHint(&out, "orders", "my-api", "production", "read_write")
	if got := out.String(); !strings.Contains(got, "gregale postgres attach orders my-api --scope production --access migration --env MIGRATION_DATABASE_URL") {
		t.Fatalf("hint = %q", got)
	}
	out.Reset()
	main.PrintAddPostgresMigrationHint(&out, "orders", "my-api", "production", "migration")
	if out.Len() != 0 {
		t.Fatalf("migration binding must not print the hint: %q", out.String())
	}
}
