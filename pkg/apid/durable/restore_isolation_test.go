// adr: 944
package durable

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity/validatorbundle"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRestoreValidatorRegistryIntegrityAndIdentity(t *testing.T) {
	bundle := validatorbundle.Bundle{AppID: uuid.NewString(), DeploymentID: uuid.NewString(), Runtime: api.ExecutionRuntimeNode22, Entrypoint: "validate.mjs", Files: []api.ExecutionFile{{Path: "validate.mjs", Content: []byte("export default () => ({protocol_version:1,valid:true})")}}}
	bundle.SHA256 = validatorBundleHash(bundle)
	write := func(value any) string {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "registry.json")
		if err = os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	loaded, err := loadDurableEntityValidatorBundles(write([]validatorbundle.Bundle{bundle}))
	if err != nil || loaded[bundle.DeploymentID].SHA256 != bundle.SHA256 {
		t.Fatal(loaded, err)
	}
	if _, err = loadDurableEntityValidatorBundles(write([]validatorbundle.Bundle{bundle, bundle})); err == nil {
		t.Fatal("accepted duplicate deployment")
	}
	bundle.Files[0].Content = []byte("changed")
	if _, err = loadDurableEntityValidatorBundles(write([]validatorbundle.Bundle{bundle})); err == nil {
		t.Fatal("accepted altered bundle")
	}
	bundle.SHA256 = validatorBundleHash(bundle)
	bundle.Files[0].Path = "../escape.mjs"
	bundle.SHA256 = validatorBundleHash(bundle)
	if _, err = loadDurableEntityValidatorBundles(write([]validatorbundle.Bundle{bundle})); err == nil {
		t.Fatal("accepted path traversal")
	}
}

func TestIsolatedRestoreHasNoApplicationFallback(t *testing.T) {
	s := &Service{Host: fakeHost{execution: true}, ValidatorBundles: map[string]validatorbundle.Bundle{}}
	// No store or normal application dispatcher is installed: missing bundles
	// must fail before either execution admission or a guest invocation.
	if _, _, err := s.enqueueIsolatedRestoreValidator(t.Context(), state.Account{}, state.App{ID: uuid.NewString()}, uuid.NewString(), "", json.RawMessage(`{}`)); err == nil {
		t.Fatal("missing bundle accepted")
	}
	appID, deploymentID := uuid.NewString(), uuid.NewString()
	s.ValidatorBundles[deploymentID] = validatorbundle.Bundle{AppID: appID, SHA256: "registered"}
	if _, _, err := s.enqueueIsolatedRestoreValidator(t.Context(), state.Account{}, state.App{ID: appID}, deploymentID, "changed", json.RawMessage(`{}`)); err == nil {
		t.Fatal("digest mismatch accepted")
	}
	if _, _, err := s.enqueueIsolatedRestoreValidator(t.Context(), state.Account{}, state.App{ID: uuid.NewString()}, deploymentID, "", json.RawMessage(`{}`)); err == nil {
		t.Fatal("wrong app accepted")
	}
}
