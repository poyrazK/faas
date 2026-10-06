//go:build !no_pg

package state_test

import (
	"context"
	"testing"
)

func TestPgProjectEnvironmentCloneCapturesAndPreparesWorkloads(t *testing.T) {
	s, _, _ := pgWithPool(t)
	projectEnvironmentCloneCapturesAndPreparesWorkloads(t, s)
}

// ADR-590: the publication gate requires the captured project configuration.
func TestPgProjectEnvironmentCloneRequiresProjectConfigurationReceipt(t *testing.T) {
	s, _, _ := pgWithPool(t)
	projectEnvironmentCloneCapturesAndPreparesWorkloads(t, s, true)
}

// ADR-590: actual sealed target values and completeness receipts are checked.
func TestPgProjectEnvironmentCloneValuePublication(t *testing.T) {
	for _, fault := range cloneValuePublicationFaults {
		t.Run(fault, func(t *testing.T) {
			s, _, _ := pgWithPool(t)
			projectEnvironmentClonePublicationContract(t, s, false, fault)
		})
	}
}

// ADR-590: raw route-row changes cannot hide behind an unchanged desired spec
// hash. Both publication gates authenticate the actual scoped policy rows.
func TestPgProjectEnvironmentCloneRoutePolicyPublication(t *testing.T) {
	for _, fault := range []string{"before_route_policy", "after_route_policy"} {
		t.Run(fault, func(t *testing.T) {
			s, _, pool := pgWithPool(t)
			projectEnvironmentClonePublicationContract(t, s, false, fault, func(ctx context.Context, appID string) error {
				_, err := pool.Exec(ctx, `update project_environment_route_policies set declared_routes='[{"path":"/changed-private-target-route","methods":["POST"]}]' where app_id=$1 and environment_slug='stage'`, appID)
				return err
			})
		})
	}
}
