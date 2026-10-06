package state_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCheckedProjectReleaseMem(t *testing.T) { checkedProjectReleaseSuite(t, state.NewMemStore()) }

func checkedProjectReleaseFixture(t *testing.T, store state.Store) (state.Account, state.Project, []state.App, []state.ProjectReleaseMember, state.ProjectReleaseSet) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, uuid.NewString()+"@graph.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "graph-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	var apps []state.App
	var members []state.ProjectReleaseMember
	for _, name := range []string{"api", "billing"} {
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, WorkloadName: name, Slug: name + "-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 2, IdleTimeoutS: 60, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, TrafficPercent: 100, TrafficPercentExplicit: true, ImageDigest: "sha256:" + name})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, dep.ID, "/graph/"+dep.ID, "graph/"+dep.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		apps = append(apps, app)
		members = append(members, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: dep.ID})
	}
	old, err := store.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, acct.ID, project.ID, "production", 1800, members)
	if err != nil {
		t.Fatal(err)
	}
	for i, app := range apps {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, TrafficPercentExplicit: true, Status: state.DeployLive, ImageDigest: "sha256:dark"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, dep.ID, "/graph/"+dep.ID, "graph/"+dep.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.(state.ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		members[i].DeploymentID = dep.ID
	}
	for _, app := range apps {
		zero := int64(0)
		if _, err := store.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, acct.ID, app.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
			t.Fatal(err)
		}
	}
	return acct, project, apps, members, old
}

func graphFences(ctx context.Context, t *testing.T, store state.Store, acct state.Account, members []state.ProjectReleaseMember) []state.BindingPromotionFence {
	t.Helper()
	var fences []state.BindingPromotionFence
	for _, member := range members {
		revision, err := store.(state.BindingPromotionStore).ReadBindingPromotionRevision(ctx, acct.ID, member.AppID)
		if err != nil {
			t.Fatal(err)
		}
		policy, err := store.(state.BindingReleasePolicyStore).GetBindingReleasePolicy(ctx, acct.ID, member.AppID, "production")
		if err != nil {
			t.Fatal(err)
		}
		fences = append(fences, state.BindingPromotionFence{AccountID: acct.ID, AppID: member.AppID, DeploymentID: member.DeploymentID, Scope: "production", Revision: revision, PolicyRevision: policy.Revision, MaxVerificationAge: time.Minute, ValidUntil: time.Now().Add(time.Minute)})
	}
	sort.Slice(fences, func(i, j int) bool { return fences[i].AppID < fences[j].AppID })
	return fences
}

func checkedProjectReleaseSuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	acct, project, apps, members, old := checkedProjectReleaseFixture(t, store)
	checked := store.(state.CheckedProjectReleaseSetStore)
	reader := store.(state.ProjectReleaseSetReader)
	unchanged := func() {
		t.Helper()
		current, err := reader.ActiveProjectReleaseSet(ctx, acct.ID, project.ID, "production")
		if err != nil || current.ID != old.ID {
			t.Fatalf("graph changed: %+v %v", current, err)
		}
		rows, err := reader.ListProjectReleaseSetsBefore(ctx, acct.ID, project.ID, "production", time.Time{}, "", 10)
		if err != nil || len(rows) != 1 {
			t.Fatalf("failed attempt leaked candidate: %+v %v", rows, err)
		}
	}
	if err := checked.CheckProjectReleaseEligibility(ctx, acct.ID, project.ID, "production", 1800, members); err != nil {
		t.Fatal(err)
	}
	unchanged()
	if _, err := store.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, acct.ID, project.ID, "production", 1800, members); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("unchecked switch: %v", err)
	}
	unchanged()
	for _, tc := range []string{"missing", "expired", "wrong-target", "wrong-scope", "wrong-policy", "waiver", "stale-revision", "wrong-active"} {
		t.Run(tc, func(t *testing.T) {
			fences := graphFences(ctx, t, store, acct, members)
			expected := old.ID
			switch tc {
			case "missing":
				fences = fences[:1]
			case "expired":
				fences[1].ValidUntil = time.Now().Add(-time.Second)
			case "wrong-target":
				fences[1].DeploymentID = uuid.NewString()
			case "wrong-scope":
				fences[1].Scope = "staging"
			case "wrong-policy":
				fences[1].PolicyRevision++
			case "waiver":
				fences[1].AllowUnsupported = true
			case "stale-revision":
				if err := store.UpsertAppEnv(ctx, acct.ID, apps[1].ID, "CHANGED", uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			case "wrong-active":
				expected = uuid.NewString()
			}
			_, err := checked.PublishProjectReleaseSetWithBindings(state.WithBindingReleaseFences(ctx, fences), acct.ID, project.ID, "production", expected, 1800, members)
			if err == nil {
				t.Fatal("invalid graph activation passed")
			}
			unchanged()
		})
	}
	fences := graphFences(ctx, t, store, acct, members)
	activated, err := checked.PublishProjectReleaseSetWithBindings(state.WithBindingReleaseFences(ctx, fences), acct.ID, project.ID, "production", old.ID, 1800, members)
	if err != nil {
		t.Fatal(err)
	}
	current, err := reader.ActiveProjectReleaseSet(ctx, acct.ID, project.ID, "production")
	if err != nil || current.ID != activated.ID || !current.Active {
		t.Fatalf("activation: %+v %v", current, err)
	}
	retired, err := reader.ProjectReleaseSetByID(ctx, acct.ID, project.ID, "production", old.ID)
	if err != nil || retired.Active || retired.ExpiresAt == nil {
		t.Fatalf("retention: %+v %v", retired, err)
	}
	fences = graphFences(ctx, t, store, acct, members)
	if _, err := checked.PublishProjectReleaseSetWithBindings(state.WithBindingReleaseFences(ctx, fences), acct.ID, project.ID, "production", old.ID, 1800, members); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale graph CAS: %v", err)
	}
	// A disabled policy still has a revision fence, but its age/ack settings
	// must not become enforced implicitly during an explicit checked release.
	policyStore := store.(state.BindingReleasePolicyStore)
	policy, err := policyStore.GetBindingReleasePolicy(ctx, acct.ID, apps[0].ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policyStore.SetBindingReleasePolicy(ctx, acct.ID, apps[0].ID, "production", api.SetBindingReleasePolicyRequest{Mode: "off", ExpectedRevision: &policy.Revision, MaxVerificationAge: "30s", RequireApplicationAck: true, Reason: "graph parity test"}); err != nil {
		t.Fatal(err)
	}
	fences = graphFences(ctx, t, store, acct, members)
	wrong := append([]state.BindingPromotionFence(nil), fences...)
	for i := range wrong {
		if wrong[i].AppID == apps[0].ID {
			wrong[i].PolicyRevision++
		}
	}
	if _, err := checked.PublishProjectReleaseSetWithBindings(state.WithBindingReleaseFences(ctx, wrong), acct.ID, project.ID, "production", activated.ID, 1800, members); err == nil {
		t.Fatal("wrong disabled-policy revision accepted")
	}
	if _, err := checked.PublishProjectReleaseSetWithBindings(state.WithBindingReleaseFences(ctx, fences), acct.ID, project.ID, "production", activated.ID, 1800, members); err != nil {
		t.Fatalf("disabled-policy restrictions enforced: %v", err)
	}
}
