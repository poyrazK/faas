package apidserver_test

import (
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"testing"
)

func TestHostingRetentionRuntimeOptIn(t *testing.T) {
	// adr: 202610171001 — independent retention opt-in is default off and hot revocable.
	for _, value := range []string{"", "false", "true", "1"} {
		manager := apidserver.NewRuntimeConfigManager(func(key string) string {
			if key == "FAAS_FRONTEND_HOSTING_RETENTION_ENABLED" {
				return value
			}
			return ""
		})
		if manager.Bool("frontend_hosting_retention_enabled", false) != (value == "true" || value == "1") {
			t.Fatal(value)
		}
		if err := manager.Apply("frontend_hosting_retention_enabled", []byte("false")); err != nil {
			t.Fatal(err)
		}
		if manager.Bool("frontend_hosting_retention_enabled", true) {
			t.Fatal("hot disable ignored")
		}
	}
}
