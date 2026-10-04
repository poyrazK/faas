//go:build !no_pg

// adr: 569
package state_test

import (
	"reflect"
	"testing"
)

func TestPgPromotionFeatureFlags(t *testing.T) {
	for _, fault := range promotionFlagCases {
		t.Run(fault, func(t *testing.T) {
			store, _, _ := pgWithPool(t)
			testPromotionFeatureFlags(t, store, fault)
		})
	}
}

func TestPgPromotionFeatureFlagSnapshotCorruption(t *testing.T) {
	for _, fault := range []string{"snapshot", "target_content", "missing_capture"} {
		t.Run(fault, func(t *testing.T) {
			store, ctx, pool := pgWithPool(t)
			mode := "success"
			if fault == "missing_capture" {
				mode = "unpublished"
			}
			f := newPromotionFlagFixture(t, store, mode)
			var err error
			switch fault {
			case "snapshot":
				_, err = pool.Exec(ctx, `update project_environment_promotion_feature_flags
					set source_snapshot=jsonb_set(source_snapshot,'{config,flags,0,default}','false'::jsonb)
					where promotion_id=$1`, f.promotion.ID)
			case "target_content":
				_, err = pool.Exec(ctx, `update feature_flag_versions
					set config=jsonb_set(config,'{flags,0,default}','true'::jsonb)
					where environment_id=$1 and version=$2`, f.target.EnvironmentID, f.targetFlags.Version)
			case "missing_capture":
				_, err = pool.Exec(ctx, `delete from project_environment_promotion_feature_flags where promotion_id=$1`, f.promotion.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := store.GetFeatureFlags(ctx, f.target, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.publish(ctx); err == nil {
				t.Fatal("corrupt or missing evidence published")
			}
			graph, err := store.ActiveProjectReleaseSet(ctx, f.account.ID, f.project.ID, "production")
			if err != nil || graph.ID != f.previous.ID {
				t.Fatalf("corrupt evidence changed graph: %v", err)
			}
			current, err := store.GetFeatureFlags(ctx, f.target, 0)
			if err != nil || current.Version != before.Version || !reflect.DeepEqual(current.Config, before.Config) {
				t.Fatalf("corrupt evidence changed target flags: %v", err)
			}
		})
	}
}
