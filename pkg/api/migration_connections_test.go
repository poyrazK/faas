package api

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMigrationServiceConnectionCheckValidation(t *testing.T) {
	valid := MigrationServiceConnectionCheck{EnvKey: "API_URL", Service: "api", DeploymentID: uuid.NewString()}
	c := MigrationDependencyChecks{ServiceConnections: []MigrationServiceConnectionCheck{valid}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, ok := ParseMigrationDependenciesCommand(MigrationDependenciesCommand(c), false); !ok {
		t.Fatal("connection check lost on command wire")
	}
	for _, key := range []string{"PORT", "GREGALE_SERVICE_API_URL", "FAAS_APP_ID", "VITE_API_URL", "NEXT_PUBLIC_API_URL", "REACT_APP_API_URL", "NUXT_PUBLIC_API_URL", "PUBLIC_API_URL", "GATSBY_API_URL", "bad-key", strings.Repeat("A", MaxSecretKeyLen+1)} {
		if ValidateMigrationConnectionKey(key) == nil {
			t.Fatalf("accepted unsupported key %q", key)
		}
	}
	c.ServiceConnections = append(c.ServiceConnections, valid)
	if c.Validate() == nil {
		t.Fatal("duplicate variable checks accepted")
	}
	valid.DeploymentID = "current"
	if (MigrationDependencyChecks{ServiceConnections: []MigrationServiceConnectionCheck{valid}}).Validate() == nil {
		t.Fatal("unpinned target accepted")
	}
}
