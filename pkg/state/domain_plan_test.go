package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDomainPlanAtomicMoveAndRollback(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			plans := raw.(state.DomainPlanStore)
			sourceID := tlsHostApp(t, ctx, raw, "")
			source, err := store.AppByID(ctx, sourceID)
			requireDomainState(t, err)
			target, err := store.CreateApp(ctx, state.App{AccountID: source.AccountID, OrgID: source.OrgID, Slug: "move-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256})
			requireDomainState(t, err)
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: target.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployPending, Scope: state.DefaultEnvScope})
			requireDomainState(t, err)
			requireDomainState(t, store.MarkDeploymentLive(ctx, dep.ID))
			sourceDep, err := store.CreateDeployment(ctx, state.Deployment{AppID: source.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:def", Status: state.DeployPending, Scope: state.DefaultEnvScope})
			requireDomainState(t, err)
			requireDomainState(t, store.MarkDeploymentLive(ctx, sourceDep.ID))
			domain := "move-" + uuid.NewString()[:8] + ".example.com"
			verifiedTLSWildcard(t, ctx, raw, domain, source.ID)
			original, err := store.DomainByName(ctx, domain)
			requireDomainState(t, err)
			expires := time.Now().UTC().Truncate(time.Microsecond).Add(24 * time.Hour)
			requireDomainState(t, store.UpdateCustomDomainCertStatus(ctx, domain, state.CustomDomainCertIssued, expires, "", time.Now().Add(-time.Minute)))
			requireDomainState(t, raw.(defaultDomainSelectionStore).SetDefaultCustomDomain(ctx, source.ID, domain))
			in := state.DomainPlanInput{AccountID: source.AccountID, OrgID: source.OrgID, AppLimit: 20, AccountLimit: 100, Manifest: api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: domain, App: target.Slug}}}, Targets: []api.DomainBindingTarget{{App: target.Slug, AppID: target.ID}}}
			plan, err := plans.PlanDomainBindings(ctx, in)
			requireDomainState(t, err)
			if plan.Changes[0].Action != "move" || !plan.Changes[0].ClearsDefault || len(plan.Changes[0].DeploymentIDs) != 1 || plan.Changes[0].DeploymentIDs[0] != dep.ID {
				t.Fatalf("move diff=%+v", plan)
			}
			before, err := store.DomainByName(ctx, domain)
			requireDomainState(t, err)
			if before.AppID != source.ID {
				t.Fatal("planning changed routing")
			}
			_, moved, err := plans.ApplyDomainBindings(ctx, in, plan.SHA256)
			requireDomainState(t, err)
			if len(moved) != 1 || moved[0].AppID != target.ID || !moved[0].Verified() || moved[0].ChallengeToken != original.ChallengeToken || moved[0].CertStatus != state.CustomDomainCertIssued || !moved[0].CertExpiresAt.Equal(expires) {
				t.Fatalf("move lost ownership or certificate: %+v", moved)
			}
			if selected, _ := raw.(state.DefaultCustomDomainStore).DefaultCustomDomain(ctx, source.ID); selected != "" {
				t.Fatal("source default survived its move")
			}
			if err := store.UpsertDoctorObservation(state.WithCustomDomainProbe(ctx, original), state.DomainDoctorObservation{Domain: domain, ObservedAt: time.Now().Add(-time.Second), DNSCheckedAt: time.Now().Add(-time.Second)}); err == nil {
				t.Fatal("pre-move probe published after move")
			}
			// Move back through the same planner, then ensure the original review
			// cannot replay across an intervening move away and back (ABA).
			back := in
			back.Manifest = api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: domain, App: source.Slug}}}
			back.Targets = []api.DomainBindingTarget{{App: source.Slug, AppID: source.ID}}
			rollback, err := plans.PlanDomainBindings(ctx, back)
			requireDomainState(t, err)
			_, _, err = plans.ApplyDomainBindings(ctx, back, rollback.SHA256)
			requireDomainState(t, err)
			if err := store.UpsertDoctorObservation(state.WithCustomDomainProbe(ctx, original), state.DomainDoctorObservation{Domain: domain, ObservedAt: time.Now().Add(-time.Second), DNSCheckedAt: time.Now().Add(-time.Second)}); err == nil {
				t.Fatal("pre-move probe survived a move away and back")
			}
			if _, _, err = plans.ApplyDomainBindings(ctx, in, plan.SHA256); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("obsolete review accepted after rollback: %v", err)
			}
			fresh, err := plans.PlanDomainBindings(ctx, in)
			requireDomainState(t, err)
			requireDomainState(t, store.DeleteCustomDomain(ctx, domain))
			_, err = store.CreateCustomDomain(ctx, domain, source.ID, original.ChallengeToken)
			requireDomainState(t, err)
			_, err = raw.MarkDomainVerifiedIfChallenge(ctx, domain, original.ChallengeToken)
			requireDomainState(t, err)
			if _, _, err := plans.ApplyDomainBindings(ctx, in, fresh.SHA256); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("recreated claim with reused DNS proof accepted old review: %v", err)
			}
		})
	}
}

func TestDomainPlanPublicationFailureRollsBackEarlierCreates(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			plans := raw.(state.DomainPlanStore)
			ownerID := tlsHostApp(t, ctx, raw, "")
			app, err := store.AppByID(ctx, ownerID)
			requireDomainState(t, err)
			foreignID := tlsHostApp(t, ctx, raw, "")
			verifiedTLSWildcard(t, ctx, raw, "*.taken.example.com", foreignID)
			in := state.DomainPlanInput{AccountID: app.AccountID, OrgID: app.OrgID, AppLimit: 20, AccountLimit: 100, Manifest: api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: "a.safe.example.com", App: app.Slug}, {Domain: "z.taken.example.com", App: app.Slug}}}, Targets: []api.DomainBindingTarget{{App: app.Slug, AppID: app.ID}, {App: app.Slug, AppID: app.ID}}}
			p, err := plans.PlanDomainBindings(ctx, in)
			requireDomainState(t, err)
			if _, _, err := plans.ApplyDomainBindings(ctx, in, p.SHA256); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("foreign wildcard publication=%v", err)
			}
			for _, name := range []string{"a.safe.example.com", "z.taken.example.com"} {
				if _, err := store.DomainByName(ctx, name); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("rejected transaction left %s attached: %v", name, err)
				}
			}
		})
	}
}

func TestDomainPlanEnvironmentMoveCapturesServingRelease(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			plans := raw.(state.DomainPlanStore)
			account, err := store.CreateAccount(ctx, "env-domain-"+uuid.NewString()+"@example.com", api.PlanPro)
			requireDomainState(t, err)
			project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "domain-project"})
			requireDomainState(t, err)
			env, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "stage"})
			requireDomainState(t, err)
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "env-domain-target", Type: state.AppTypeApp, RAMMB: 256, Manifest: state.AppManifest{RevisionPinTTLSeconds: 300}})
			requireDomainState(t, err)
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployPending, Scope: "stage"})
			requireDomainState(t, err)
			requireDomainState(t, store.MarkDeploymentLive(ctx, dep.ID))
			verifiedTLSWildcard(t, ctx, raw, "env-move.example.com", app.ID)
			in := state.DomainPlanInput{AccountID: account.ID, OrgID: app.OrgID, AppLimit: 20, AccountLimit: 100, Manifest: api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: "env-move.example.com", App: app.Slug, Environment: "stage"}}}, Targets: []api.DomainBindingTarget{{App: app.Slug, AppID: app.ID, Environment: "stage", EnvironmentID: env.ID}}}
			p, err := plans.PlanDomainBindings(ctx, in)
			requireDomainState(t, err)
			release, err := raw.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, account.ID, project.ID, "stage", 300, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}})
			requireDomainState(t, err)
			if release.ID == "" {
				t.Fatal("release not published")
			}
			if _, _, err := plans.ApplyDomainBindings(ctx, in, p.SHA256); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("changed release accepted old review: %v", err)
			}
			p, err = plans.PlanDomainBindings(ctx, in)
			requireDomainState(t, err)
			_, domains, err := plans.ApplyDomainBindings(ctx, in, p.SHA256)
			requireDomainState(t, err)
			if domains[0].EnvironmentID != env.ID || domains[0].ChallengeToken != "tok" || !domains[0].Verified() {
				t.Fatalf("environment move=%+v", domains)
			}
		})
	}
}

func TestDomainPlanBatchQuotaAndStaleClaimLeaveEverythingUnchanged(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			plans := raw.(state.DomainPlanStore)
			appID := tlsHostApp(t, ctx, raw, "")
			app, err := store.AppByID(ctx, appID)
			requireDomainState(t, err)
			in := state.DomainPlanInput{AccountID: app.AccountID, OrgID: app.OrgID, AppLimit: 1, AccountLimit: 100, Manifest: api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: "a.plan.example.com", App: app.Slug}, {Domain: "b.plan.example.com", App: app.Slug}}}, Targets: []api.DomainBindingTarget{{App: app.Slug, AppID: app.ID}, {App: app.Slug, AppID: app.ID}}}
			if _, err := plans.PlanDomainBindings(ctx, in); !errors.Is(err, state.ErrCustomDomainQuotaExceeded) {
				t.Fatalf("batch quota=%v", err)
			}
			in.AppLimit = 20
			plan, err := plans.PlanDomainBindings(ctx, in)
			requireDomainState(t, err)
			_, err = store.CreateCustomDomain(ctx, "b.plan.example.com", app.ID, "intervening")
			requireDomainState(t, err)
			if _, _, err := plans.ApplyDomainBindings(ctx, in, plan.SHA256); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("stale plan=%v", err)
			}
			if _, err := store.DomainByName(ctx, "a.plan.example.com"); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("stale batch partially created its first domain")
			}
			fresh, err := plans.PlanDomainBindings(ctx, in)
			requireDomainState(t, err)
			_, domains, err := plans.ApplyDomainBindings(ctx, in, fresh.SHA256)
			requireDomainState(t, err)
			if len(domains) != 2 || domains[0].Verified() || domains[0].ChallengeToken == "" || domains[1].ChallengeToken != "intervening" {
				t.Fatalf("applied create/noop=%+v", domains)
			}
		})
	}
}

func TestDomainPlanRejectsPendingMoveAndUnservedDestination(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			plans := raw.(state.DomainPlanStore)
			sourceID := tlsHostApp(t, ctx, raw, "")
			source, err := store.AppByID(ctx, sourceID)
			requireDomainState(t, err)
			target, err := store.CreateApp(ctx, state.App{AccountID: source.AccountID, OrgID: source.OrgID, Slug: "unserved-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256})
			requireDomainState(t, err)
			d, err := store.CreateCustomDomain(ctx, "unsafe.plan.example.com", source.ID, "proof")
			requireDomainState(t, err)
			in := state.DomainPlanInput{AccountID: source.AccountID, OrgID: source.OrgID, AppLimit: 20, AccountLimit: 100, Manifest: api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: d.Domain, App: target.Slug}}}, Targets: []api.DomainBindingTarget{{App: target.Slug, AppID: target.ID}}}
			if _, err := plans.PlanDomainBindings(ctx, in); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("pending move=%v", err)
			}
			_, err = raw.MarkDomainVerifiedIfChallenge(ctx, d.Domain, d.ChallengeToken)
			requireDomainState(t, err)
			if _, err := plans.PlanDomainBindings(ctx, in); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("unserved move=%v", err)
			}
		})
	}
}
