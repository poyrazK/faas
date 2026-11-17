package imaged

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A companion over the filesystem cap used to fail with the plan's detail
// alone ("requires 2112 MB"), so a deployment with several images did not say
// which one to shrink (Kroki feedback 2026-11-17).
func TestMarkDeployFailedNamesTooLargeWorkload(t *testing.T) {
	limits, ok := api.LimitsFor(api.PlanFree)
	if !ok {
		t.Fatal("no Free plan limits")
	}
	sidecars, _ := json.Marshal(api.Sidecars{{Name: "mermaid", Image: "example.com/mermaid:1"}})
	for _, tc := range []struct {
		name       string
		wrap       func(error, state.Deployment) error
		wantPrefix string
		wantFix    string
	}{
		{
			name: "companion",
			wrap: func(err error, _ state.Deployment) error {
				return fmt.Errorf("imaged: %w", attributeCompanion(err, "mermaid", "example.com/mermaid@sha256:abc"))
			},
			wantPrefix: `companion "mermaid" (image example.com/mermaid@sha256:abc): `,
			wantFix:    `Shrink or remove companion "mermaid"`,
		},
		{
			name: "app image beside companions",
			wrap: func(err error, dep state.Deployment) error {
				return fmt.Errorf("imaged: build app layer: %w", attributeAppImage(err, dep))
			},
			wantPrefix: "app image (image sha256:feed): ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "attr@example.com", api.PlanFree)
			if err != nil {
				t.Fatalf("CreateAccount: %v", err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "kroki", Type: state.AppTypeApp})
			if err != nil {
				t.Fatalf("CreateApp: %v", err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:feed", Sidecars: sidecars})
			if err != nil {
				t.Fatalf("CreateDeployment: %v", err)
			}
			problem := api.ErrAppLayerTooLarge(limits, 2112<<20)
			h := &Handler{store: store}
			if err := h.markDeployFailed(ctx, dep.ID, tc.wrap(problem, dep), ""); err != nil {
				t.Fatalf("markDeployFailed: %v", err)
			}
			got, err := store.DeploymentByID(ctx, dep.ID)
			if err != nil {
				t.Fatalf("DeploymentByID: %v", err)
			}
			if got.ErrorCode != api.CodeAppLayerTooBig {
				t.Fatalf("code = %q, want %q", got.ErrorCode, api.CodeAppLayerTooBig)
			}
			if got.Error != tc.wantPrefix+problem.Detail {
				t.Fatalf("detail = %q, want %q", got.Error, tc.wantPrefix+problem.Detail)
			}
			if tc.wantFix != "" && !strings.Contains(got.ErrorFix, tc.wantFix) {
				t.Fatalf("fix = %q, want it to contain %q", got.ErrorFix, tc.wantFix)
			}
		})
	}
}

// Without companions the app image is the only image: no prefix.
func TestAttributeAppImageWithoutCompanions(t *testing.T) {
	err := fmt.Errorf("x")
	if got := attributeAppImage(err, state.Deployment{ImageDigest: "sha256:feed"}); got != err {
		t.Fatalf("attributeAppImage without companions = %v, want the error unchanged", got)
	}
	if got := attributeAppImage(err, state.Deployment{Sidecars: json.RawMessage("[]")}); got != err {
		t.Fatalf("attributeAppImage with empty companions = %v, want the error unchanged", got)
	}
}
