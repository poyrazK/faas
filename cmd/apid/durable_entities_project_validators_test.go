// adr: 948
package main

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity/validatorbundle"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestCopiedDeploymentValidatorBindingAndInspection(t *testing.T) {
	app := state.App{ID: uuid.NewString()}
	source := state.Deployment{ID: uuid.NewString(), AppID: app.ID}
	target := uuid.NewString()
	bucket := &entityTestBucket{objects: map[string]entityTestObject{}}
	shared := validatorbundle.NewArtifacts(bucket, map[string]bool{app.ID: true})
	s := &server{durableEntityValidatorReleaseGateEnabled: true, durableEntityRestoreIsolationEnabled: true, durableEntityApps: map[string]bool{app.ID: true}, durableEntityValidatorArtifacts: shared}
	ctx := s.validatorPromotionContext(t.Context())
	if err := transferPromotionValidator(ctx, source, target); err == nil {
		t.Fatal("missing source binding copied")
	}
	b := validatorbundle.Bundle{AppID: app.ID, DeploymentID: source.ID, Runtime: api.ExecutionRuntimeNode22, Entrypoint: "v.mjs", Files: []api.ExecutionFile{{Path: "v.mjs", Content: []byte("export default ()=>({protocol_version:1,valid:true})")}}}
	b.SHA256 = validatorbundle.Hash(b)
	if err := shared.Publish(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := transferPromotionValidator(ctx, source, target); err != nil {
		t.Fatal(err)
	}
	info := s.deploymentValidatorInfo(ctx, state.Deployment{ID: target, AppID: app.ID}, app)
	if info == nil || info.Status != "ready" || info.SHA256 != b.SHA256 || info.Source != "object_storage" {
		t.Fatal(info)
	}
	missing := s.deploymentValidatorInfo(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID}, app)
	if missing == nil || missing.Status != "unavailable" || missing.SHA256 != "" {
		t.Fatal(missing)
	}
}
