// adr: 233, 375
package gateway

import "github.com/onebox-faas/faas/pkg/hostidentity"

// BuildEnvironmentHost returns the stable URL identity shared with management.
func BuildEnvironmentHost(suffix, environmentID, appID string) string {
	return hostidentity.BuildEnvironmentHost(suffix, environmentID, appID)
}

// EnvironmentIDsFromHost accepts the canonical environment/workload label.
func EnvironmentIDsFromHost(suffix, host string) (environmentID, appID string, ok bool) {
	return hostidentity.EnvironmentIDsFromHost(suffix, host)
}
