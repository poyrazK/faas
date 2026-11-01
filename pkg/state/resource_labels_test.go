package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/labels"
	"github.com/onebox-faas/faas/pkg/state"
)

type resourceLabelTestStore interface {
	state.ResourceLabelStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
	CreateCron(ctx context.Context, appID, schedule, path string, enabled bool) (state.Cron, error)
	JobCreate(ctx context.Context, accountID, name, kind, imageRef string, command []string, ramMB, taskTimeoutSec, maxParallelism, retryMax int, envOverrides json.RawMessage) (state.Job, error)
}

// Cron and job labels write atomically, keep each kind apart and never pass
// the per-resource limit (ADR-20261031100025 step 5).
func TestResourceLabelStore(t *testing.T) {
	stores := map[string]func(*testing.T) (resourceLabelTestStore, context.Context){
		"mem": func(*testing.T) (resourceLabelTestStore, context.Context) {
			return state.NewMemStore(), context.Background()
		},
		"pg": func(t *testing.T) (resourceLabelTestStore, context.Context) {
			s, ctx := pgStore(t)
			return s, ctx
		},
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			s, ctx := open(t)
			acct, err := s.CreateAccount(ctx, "resource-labels-"+name+"@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "resource-labels", RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			cron, err := s.CreateCron(ctx, app.ID, "* * * * *", "/tick", true)
			if err != nil {
				t.Fatal(err)
			}
			job, err := s.JobCreate(ctx, acct.ID, "labelled", "batch", "registry.example/job:v1", []string{"/bin/true"}, 128, 30, 1, 0, nil)
			if err != nil {
				t.Fatal(err)
			}

			got, err := s.UpdateResourceLabels(ctx, state.LabelKindCron, cron.ID, labels.Change{Set: map[string]string{"team": "billing", "canary": ""}})
			if want := map[string]string{"team": "billing", "canary": ""}; err != nil || !maps.Equal(got, want) {
				t.Fatalf("cron write = %v, %v; want %v", got, err, want)
			}
			got, err = s.UpdateResourceLabels(ctx, state.LabelKindCron, cron.ID, labels.Change{Set: map[string]string{"tier": "batch"}, Remove: []string{"canary"}})
			if want := map[string]string{"team": "billing", "tier": "batch"}; err != nil || !maps.Equal(got, want) {
				t.Fatalf("cron set and remove = %v, %v; want %v", got, err, want)
			}
			if _, err := s.UpdateResourceLabels(ctx, state.LabelKindJob, job.ID, labels.Change{Set: map[string]string{"team": "search"}}); err != nil {
				t.Fatal(err)
			}

			byID, err := s.ResourceLabelsByID(ctx, state.LabelKindCron, []string{cron.ID, job.ID, "not-a-uuid"})
			if err != nil || len(byID) != 1 || byID[cron.ID]["tier"] != "batch" {
				t.Fatalf("cron ResourceLabelsByID = %v, %v; want the cron alone", byID, err)
			}
			if l, err := s.ResourceLabels(ctx, state.LabelKindJob, job.ID); err != nil || l["team"] != "search" {
				t.Fatalf("job labels = %v, %v", l, err)
			}

			full := map[string]string{}
			for i := range api.LabelsPerResourceMax {
				full[fmt.Sprintf("k%d", i)] = "v"
			}
			if _, err := s.UpdateResourceLabels(ctx, state.LabelKindJob, job.ID, labels.Change{Set: full, Remove: []string{"team"}}); err != nil {
				t.Fatalf("a write to the limit: %v", err)
			}
			var le *labels.Error
			if _, err := s.UpdateResourceLabels(ctx, state.LabelKindJob, job.ID, labels.Change{Set: map[string]string{"one-more": "v"}}); !errors.As(err, &le) || le.Code != labels.CodeLimitExceeded {
				t.Fatalf("over the limit = %v; want %s", err, labels.CodeLimitExceeded)
			}
			if _, err := s.ResourceLabels(ctx, state.LabelKind("bucket"), job.ID); err == nil {
				t.Fatal("an unknown kind was accepted")
			}
		})
	}
}

// A database that does not exist has no labels to read or write in Postgres.
func TestResourceLabelStoreMissingDatabase(t *testing.T) {
	s, ctx := pgStore(t)
	const missing = "00000000-0000-4000-8000-000000000001"
	if _, err := s.ResourceLabels(ctx, state.LabelKindDatabase, missing); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing database labels = %v; want ErrNotFound", err)
	}
	if _, err := s.UpdateResourceLabels(ctx, state.LabelKindDatabase, missing, labels.Change{Set: map[string]string{"a": "b"}}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing database write = %v; want ErrNotFound", err)
	}
}
