//go:build !no_pg

package globaldirectory

// adr: 797
import "testing"

func TestGlobalAppProvisioningPostgresContract(t *testing.T) {
	pool := directoryPG(t)
	s := NewPgStore(pool)
	appProvisionContract(t, s, directoryAccount(t, pool))
	for _, sql := range []string{"UPDATE global_app_provisioning SET api_base='https://replacement.example.com'", "DELETE FROM global_app_provisioning", "TRUNCATE global_app_provisioning"} {
		if _, err := pool.Exec(t.Context(), sql); err == nil {
			t.Fatalf("intent guard allowed %s", sql)
		}
	}
}
