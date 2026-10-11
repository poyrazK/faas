package conformance

// adr: 791
import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// Unissued metadata cannot grant capacity, fabricate a native source, or
// produce a failure/retirement receipt in either implementation.
func testRegionalRuntimeUnissuedRequests(t *testing.T, fx *Fixture) {
	appID := uuid.MustParse(fx.App.ID).String()
	nodeID := uuid.MustParse(fx.Node.ID).String()
	p, source := runtimequota.Placement{}, runtimequota.LaunchSource{}
	refused := []struct {
		name string
		call func() error
	}{
		{"placement", func() error { return fx.Store.IssueRegionalRuntimePlacement(fx.Ctx, p) }},
		{"launch", func() error { return fx.Store.IssueRegionalRuntimeLaunch(fx.Ctx, p, source) }},
		{"resources", func() error {
			return fx.Store.IssueRegionalRuntimeLaunchResources(fx.Ctx, p, source, state.RegionalRuntimeResources{})
		}},
		{"issued check", func() error { return fx.Store.CheckRegionalRuntimeIssuedPlacement(fx.Ctx, p) }},
		{"resume", func() error {
			_, err := fx.Store.IssueRegionalRuntimeServingResume(fx.Ctx, runtimequota.ServingResumeIntent{})
			return err
		}},
		{"begin failure", func() error {
			_, err := fx.Store.BeginRegionalRuntimeLaunchFailure(fx.Ctx, source, uuid.NewString())
			return err
		}},
		{"fail attempt", func() error { return fx.Store.FailRegionalRuntimeAttempt(fx.Ctx, source, string(state.StateWaking)) }},
	}
	for _, request := range refused {
		if err := request.call(); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("%s accepted malformed ownership: %v", request.name, err)
		}
	}
	checkMissing := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("%s invented unissued authority: %v", name, err)
		}
	}
	_, err := fx.Store.RegionalRuntimeCapacity(fx.Ctx, appID, uuid.NewString())
	checkMissing("capacity", err)
	_, err = fx.Store.RegionalRuntimeLaunchSource(fx.Ctx, appID, uuid.NewString())
	checkMissing("native source", err)
	_, err = fx.Store.RegionalRuntimeQuotaHead(fx.Ctx, appID)
	checkMissing("quota head", err)
	_, err = fx.Store.RegionalRuntimeServingResume(fx.Ctx, appID, uuid.NewString())
	checkMissing("resume", err)
	_, err = fx.Store.HeldRegionalRuntimeServingResumeForInstance(fx.Ctx, appID, uuid.NewString(), nodeID, uuid.NewString())
	checkMissing("held resume", err)
	inventory, err := fx.Store.ListRegionalRuntimeInventory(fx.Ctx, "", state.RegionalRuntimeRetirementCursor{}, 10)
	if err != nil || len(inventory) != 0 {
		t.Fatalf("refused requests created physical holds: %+v %v", inventory, err)
	}
	retirements, err := fx.Store.ListRegionalRuntimeRetirements(fx.Ctx, uuid.NewString(), state.RegionalRuntimeRetirementCursor{}, 10)
	if err != nil || len(retirements) != 0 {
		t.Fatalf("refused requests created retirement authority: %+v %v", retirements, err)
	}
	instance, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID, string(state.StateWaking), fx.App.RAMMB, fx.Node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	stored, err := fx.Store.JobExecutionInstanceByID(fx.Ctx, instance.ID)
	if err != nil || stored.ID != instance.ID || stored.JobID != instance.JobID || stored.NodeID != fx.Node.ID || stored.State != instance.State || stored.RAMMB != instance.RAMMB || stored.Kind != instance.Kind {
		t.Fatalf("job boot reader changed original persisted identity: %+v %v", stored, err)
	}
}
