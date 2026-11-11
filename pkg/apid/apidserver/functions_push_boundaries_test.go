// A functions-project push applies its function's settings and env. A
// release staged without traffic must not change what the live release runs
// with (apps/projects review 2026-11-10, P11).
package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func TestPushSettingsManifestDropsFunctionSettingsFromStagedReleases(t *testing.T) {
	manifest := &gregalemanifest.Manifest{FunctionSettings: &gregalemanifest.FunctionSpec{RAMMB: 512}}
	if got := apidserver.PushSettingsManifest(manifest, apidsource.EnqueueParams{}); got.FunctionSettings == nil {
		t.Fatal("a release taking traffic keeps the function's settings")
	}
	if got := apidserver.PushSettingsManifest(manifest, apidsource.EnqueueParams{TrafficPercentExplicit: true, TrafficPercent: 10}); got.FunctionSettings == nil {
		t.Fatal("a release taking some traffic keeps the function's settings")
	}
	if got := apidserver.PushSettingsManifest(manifest, apidsource.EnqueueParams{TrafficPercentExplicit: true}); got.FunctionSettings != nil {
		t.Fatal("a release staged without traffic must not apply the function's settings")
	}
	if manifest.FunctionSettings == nil {
		t.Fatal("the caller's manifest must not be modified")
	}
}
