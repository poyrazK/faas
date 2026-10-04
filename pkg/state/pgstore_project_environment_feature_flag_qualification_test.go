//go:build !no_pg

// adr: 569
package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgQualificationPinsFeatureFlags(t *testing.T) {
	s, _, _ := pgWithPool(t)
	qualificationPinsFeatureFlags(t, s)
}

func TestPgQualificationAuthenticatesFeatureFlagContents(t *testing.T) {
	s, _, pool := pgWithPool(t)
	qualificationPinsFeatureFlags(t, s, func(ctx context.Context, scope state.FeatureFlagScope, version state.FeatureFlagVersion) error {
		_, err := pool.Exec(ctx, "update feature_flag_versions set config=jsonb_set(config,'{flags,0,default}','false'::jsonb) where environment_id=$1 and version=$2", scope.EnvironmentID, version.Version)
		return err
	})
}
