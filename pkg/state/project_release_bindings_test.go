package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCheckedBindingReleaseGraphMem(t *testing.T) {
	checkedBindingReleaseGraphSuite(t, state.NewMemStore())
}
func TestCheckedBindingReleaseGraphPG(t *testing.T) {
	store, _ := pgStore(t)
	checkedBindingReleaseGraphSuite(t, store)
}

func checkedBindingReleaseGraphSuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@checked-graph.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "checked-graph"})
	if err != nil {
		t.Fatal(err)
	}
	var members []state.ProjectReleaseMember
	var apps []state.App
	for _, name := range []string{"graph-api", "graph-billing"} {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: name, WorkloadName: name, Type: state.AppTypeApp, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "production", ImageDigest: "sha256:" + name})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/test/rootfs", "test/"+name, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		apps = append(apps, app)
		members = append(members, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: deployment.ID})
	}
	graphs := store.(state.ProjectReleaseSetStore)
	first, err := graphs.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, members)
	if err != nil {
		t.Fatal(err)
	}
	policies := store.(state.BindingReleasePolicyStore)
	for _, app := range apps {
		zero := int64(0)
		if _, err := policies.SetBindingReleasePolicy(ctx, account.ID, app.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
			t.Fatal(err)
		}
	}
	capture := func() []state.BindingPromotionFence {
		fences := make([]state.BindingPromotionFence, 0, len(members))
		for _, member := range members {
			revision, err := store.(state.BindingPromotionStore).ReadBindingPromotionRevision(ctx, account.ID, member.AppID)
			if err != nil {
				t.Fatal(err)
			}
			policy, err := policies.GetBindingReleasePolicy(ctx, account.ID, member.AppID, "production")
			if err != nil {
				t.Fatal(err)
			}
			fences = append(fences, state.BindingPromotionFence{AccountID: account.ID, AppID: member.AppID, DeploymentID: member.DeploymentID, Scope: "production", Revision: revision, ValidUntil: time.Now().Add(time.Minute), PolicyRevision: policy.Revision, MaxVerificationAge: func() time.Duration { age, _ := time.ParseDuration(policy.MaxVerificationAge); return age }()})
		}
		return fences
	}
	checked := store.(state.CheckedProjectReleaseStore)
	for _, scenario := range []string{"missing member fence", "wrong target", "expired", "changed policy", "CAS"} {
		t.Run(scenario, func(t *testing.T) {
			fences := capture()
			expected := first.ID
			switch scenario {
			case "missing member fence":
				fences = fences[:1]
			case "wrong target":
				fences[1].DeploymentID = uuid.NewString()
			case "expired":
				fences[1].ValidUntil = time.Now().Add(-time.Second)
			case "changed policy":
				expectedRevision := fences[1].PolicyRevision
				if _, err := policies.SetBindingReleasePolicy(ctx, account.ID, apps[1].ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &expectedRevision, MaxVerificationAge: "30s"}); err != nil {
					t.Fatal(err)
				}
			case "CAS":
				expected = uuid.NewString()
			}
			if _, err := checked.ActivateProjectReleaseWithBindings(ctx, account.ID, project.ID, "production", expected, nil, 1800, members, fences); err == nil {
				t.Fatal("invalid graph activated")
			}
			active, err := store.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
			if err != nil || active.ID != first.ID {
				t.Fatalf("failure changed active graph: %+v %v", active, err)
			}
		})
	}
	originalMembers := append([]state.ProjectReleaseMember(nil), members...)
	for i, app := range apps {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "production", TrafficPercentExplicit: true, ImageDigest: "sha256:replacement-" + app.Slug})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, dep.ID, "/replacement/rootfs", "test/replacement-"+app.Slug, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.(state.ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		members[i].DeploymentID = dep.ID
	}
	replacementMembers := append([]state.ProjectReleaseMember(nil), members...)
	second, err := checked.ActivateProjectReleaseWithBindings(ctx, account.ID, project.ID, "production", first.ID, nil, 1800, members, capture())
	if err != nil {
		t.Fatal(err)
	}
	if !second.Active || second.ID == first.ID {
		t.Fatalf("activation=%+v", second)
	}
	old, err := store.(state.ProjectReleaseSetReader).ProjectReleaseSetByID(ctx, account.ID, project.ID, "production", first.ID)
	if err != nil || old.Active || old.ExpiresAt == nil {
		t.Fatalf("old release=%+v %v", old, err)
	}

	members = originalMembers
	for _, scenario := range []string{"active source", "wrong source", "wrong environment", "substituted member", "stale current", "expired evidence", "missing fence"} {
		t.Run("rollback "+scenario, func(t *testing.T) {
			source, expected, environment := first.ID, second.ID, "production"
			selected := append([]state.ProjectReleaseMember(nil), members...)
			fences := capture()
			switch scenario {
			case "active source":
				source = second.ID
			case "wrong source":
				source = uuid.NewString()
			case "wrong environment":
				environment = "staging"
			case "substituted member":
				selected[0].DeploymentID = replacementMembers[0].DeploymentID
				fences[0].DeploymentID = selected[0].DeploymentID
			case "stale current":
				expected = uuid.NewString()
			case "expired evidence":
				fences[0].ValidUntil = time.Now().Add(-time.Second)
			case "missing fence":
				fences = fences[:1]
			}
			if _, err := checked.RollbackProjectReleaseWithBindings(ctx, account.ID, project.ID, environment, expected, source, 1800, selected, fences); err == nil {
				t.Fatal("invalid recovery accepted")
			}
			active, err := store.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
			if err != nil || active.ID != second.ID {
				t.Fatalf("failed recovery changed routing: %+v %v", active, err)
			}
		})
	}
	recovered, err := checked.RollbackProjectReleaseWithBindings(ctx, account.ID, project.ID, "production", second.ID, first.ID, 1800, members, capture())
	if err != nil {
		t.Fatal(err)
	}
	reader := store.(state.ProjectReleaseSetReader)
	persisted, err := reader.ProjectReleaseSetByID(ctx, account.ID, project.ID, "production", recovered.ID)
	if err != nil || persisted.RollbackSourceReleaseID != first.ID || persisted.RollbackPreviousReleaseID != second.ID || !persisted.Active {
		t.Fatalf("recovery receipt=%+v %v", persisted, err)
	}
	if _, err := checked.RollbackProjectReleaseWithBindings(ctx, account.ID, project.ID, "production", second.ID, first.ID, 1800, members, capture()); err == nil {
		t.Fatal("stale recovery replay accepted")
	}
	// Keep deployments live and retained independently; expired graph history still cannot authorize recovery.
	short, err := checked.ActivateProjectReleaseWithBindings(ctx, account.ID, project.ID, "production", recovered.ID, nil, 1, members, capture())
	if err != nil {
		t.Fatal(err)
	}
	current, err := checked.ActivateProjectReleaseWithBindings(ctx, account.ID, project.ID, "production", short.ID, nil, 1800, members, capture())
	if err != nil {
		t.Fatal(err)
	}
	retired, err := reader.ProjectReleaseSetByID(ctx, account.ID, project.ID, "production", short.ID)
	if err != nil {
		t.Fatal(err)
	}
	<-time.After(time.Until(retired.ExpiresAt.Add(20 * time.Millisecond)))
	if _, err := checked.RollbackProjectReleaseWithBindings(ctx, account.ID, project.ID, "production", current.ID, short.ID, 1800, members, capture()); err == nil {
		t.Fatal("expired historical graph recovered")
	}
	active, err := reader.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
	if err != nil || active.ID != current.ID {
		t.Fatalf("expired history changed routing: %+v %v", active, err)
	}
	if _, err := graphs.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, members); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("legacy bypass: %v", err)
	}
	err = store.(state.ProjectReleaseSetPromotionStore).DeactivateProjectReleaseSetIfActive(ctx, account.ID, project.ID, "production", second.ID, members)
	if !state.IsBindingReleaseRequired(err) && !errors.Is(err, state.ErrConflict) {
		t.Fatalf("deactivation bypass: %v", err)
	}
}
