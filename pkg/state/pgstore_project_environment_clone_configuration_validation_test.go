//go:build !no_pg

// adr: 590
package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCloneSourceConfigurationValidationDetectsDriftWithoutRecapture(t *testing.T) {
	for _, fault := range []string{"variable", "secret", "config", "flag", "settings", "artifact", "release", "workload_roster"} {
		t.Run(fault, func(t *testing.T) {
			s, ctx, pool := pgWithPool(t)
			a, err := s.CreateAccount(ctx, "configuration-validation@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "validation"})
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "validation-api", WorkloadName: "validation-api", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1,
				Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
			if err != nil {
				t.Fatal(err)
			}
			deploy := func(app state.App, digest string) state.Deployment {
				t.Helper()
				d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: digest})
				if err != nil {
					t.Fatal(err)
				}
				if err := s.SetDeploymentRootfs(ctx, d.ID, "/validation.ext4", "layers/"+d.ID, 4096); err != nil {
					t.Fatal(err)
				}
				if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
					t.Fatal(err)
				}
				return d
			}
			d := deploy(app, "sha256:original")
			if err := s.UpsertAppEnvInScope(ctx, a.ID, app.ID, "production", "VERSION", "original"); err != nil {
				t.Fatal(err)
			}
			if err := s.UpsertAppSecretWithKidAndValueHashInScope(ctx, a.ID, app.ID, "production", "TOKEN", "kid", strings.Repeat("a", 16), []byte("original-sealed")); err != nil {
				t.Fatal(err)
			}
			env, err := s.ProjectEnvironmentBySlug(ctx, a.ID, p.ID, "production")
			if err != nil {
				t.Fatal(err)
			}
			flagScope := state.FeatureFlagScope{AccountID: a.ID, ProjectID: p.ID, EnvironmentID: env.ID}
			flags, _ := cloneFlagFixture(t, s, flagScope)
			op, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneCaptureRequest{
				AccountID: a.ID, ProjectID: p.ID, SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "validation"})
			if err != nil {
				t.Fatal(err)
			}
			lease, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			lease.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationCapturing, lease.Operation.Revision, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			original, err := s.ValidateProjectEnvironmentCloneSourceConfigurationForLease(ctx, lease)
			if err != nil || original.Hash != op.SourceRevisionHash {
				t.Fatalf("unchanged source failed validation: %v", err)
			}
			stale := lease
			stale.Token = uuid.NewString()
			if _, err := s.ValidateProjectEnvironmentCloneSourceConfigurationForLease(ctx, stale); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("stale worker validated source: %v", err)
			}
			switch fault {
			case "variable":
				err = s.UpsertAppEnvInScope(ctx, a.ID, app.ID, "production", "VERSION", "changed")
			case "secret":
				err = s.UpsertAppSecretWithKidAndValueHashInScope(ctx, a.ID, app.ID, "production", "TOKEN", "kid", strings.Repeat("b", 16), []byte("changed-sealed"))
			case "config":
				var values []byte
				var hash string
				values, hash, err = api.NormalizeProjectEnvironmentConfig([]byte(`{"region":"changed"}`))
				if err == nil {
					_, err = s.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{AccountID: a.ID, ProjectID: p.ID, EnvironmentSlug: "production", ConfigHash: hash, Values: values})
				}
			case "flag":
				flags.Flags[0].Description = "changed"
				_, err = s.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: flagScope, ExpectedVersion: flags.Version, Config: flags.Config, Actor: "developer"})
			case "settings":
				_, err = pool.Exec(ctx, "update apps set ram_mb=512 where id=$1", app.ID)
			case "artifact":
				deploy(app, "sha256:changed")
			case "release":
				_, err = s.PublishProjectReleaseSet(ctx, a.ID, p.ID, "production", 3600, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: d.ID}})
			case "workload_roster":
				var extra state.App
				extra, err = s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "validation-jobs", WorkloadName: "validation-jobs", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
				if err == nil {
					deploy(extra, "sha256:jobs")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if result, err := s.ValidateProjectEnvironmentCloneSourceConfigurationForLease(ctx, lease); !errors.Is(err, state.ErrConflict) || result != (state.ProjectEnvironmentCloneConfigurationCapture{}) {
				t.Fatalf("%s returned usable validation: %+v %v", fault, result, err)
			}
			retained, err := s.ProjectEnvironmentCloneConfigurationForLease(ctx, lease)
			if err != nil || retained != original {
				t.Fatalf("drift replaced original configuration: %+v %v", retained, err)
			}
			current, err := s.ProjectEnvironmentCloneOperationByID(ctx, a.ID, p.ID, op.ID)
			if err != nil || current.Status != state.CloneOperationCapturing || current.Revision != lease.Operation.Revision || len(current.Resources) != 0 || current.SourceRevisionHash != op.SourceRevisionHash {
				t.Fatalf("validation advanced capture: %+v %v", current, err)
			}
			if _, err := s.ProjectEnvironmentBySlug(ctx, a.ID, p.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("validation created a target")
			}
		})
	}
}
