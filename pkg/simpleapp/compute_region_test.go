package simpleapp

import "testing"

func TestComputeRegionPlanPreservesCreationIntent(t *testing.T) {
	for _, region := range []string{"", "eu-fsn", "future-region"} {
		plan, err := Resolve(Spec{Slug: "regional", ComputeRegion: region})
		if err != nil || plan.ComputeRegion != region || plan.CreateRequest().ComputeRegion != region {
			t.Fatalf("region=%q plan=%+v err=%v", region, plan, err)
		}
	}
	for _, region := range []string{"EU-fsn", " eu-fsn", "eu_fsn"} {
		if _, err := Resolve(Spec{Slug: "regional", ComputeRegion: region}); err == nil {
			t.Fatalf("accepted noncanonical region %q", region)
		}
	}
}
