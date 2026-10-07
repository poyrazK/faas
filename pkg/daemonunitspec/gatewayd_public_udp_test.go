package daemonunitspec

import (
	"strings"
	"testing"
)

func TestGatewaydPublicLoadsOptionalUDPEnvironment(t *testing.T) {
	// Optional loading preserves default-disabled startup; enabled deployments
	// must still receive the policy rendered into /etc/faas/udpd.env.
	for _, file := range strings.Fields(UnitGatewaydPublic().EnvironmentFile) {
		if file == "-/etc/faas/udpd.env" {
			return
		}
	}
	t.Fatal("public gateway must load optional UDP policy environment")
}
