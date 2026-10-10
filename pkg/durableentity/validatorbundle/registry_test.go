// adr: 946
package validatorbundle

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
)

func TestMergeIsStableAndRejectsDeploymentRebinding(t *testing.T) {
	makeBundle := func() Bundle {
		b := Bundle{AppID: uuid.NewString(), DeploymentID: uuid.NewString(), Runtime: api.ExecutionRuntimeNode22, Entrypoint: "validator.mjs", Files: []api.ExecutionFile{{Path: "validator.mjs", Content: []byte("export default input => ({protocol_version:1,valid:true})")}}}
		b.SHA256 = Hash(b)
		return b
	}
	a, b := makeBundle(), makeBundle()
	forward, err := Merge(map[string]Bundle{a.DeploymentID: a}, b)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := Merge(map[string]Bundle{b.DeploymentID: b}, a)
	if err != nil || !bytes.Equal(forward, reverse) {
		t.Fatal("unstable registry ordering", err)
	}
	var entries []Bundle
	if err = json.Unmarshal(forward, &entries); err != nil || len(entries) != 2 {
		t.Fatal(err)
	}
	if _, err = Merge(map[string]Bundle{a.DeploymentID: a}, a); err != nil {
		t.Fatal("idempotent packaging rejected", err)
	}
	changed := a
	changed.Files = []api.ExecutionFile{{Path: "validator.mjs", Content: []byte("changed")}}
	changed.SHA256 = Hash(changed)
	if _, err = Merge(map[string]Bundle{a.DeploymentID: a}, changed); err == nil {
		t.Fatal("deployment rebound")
	}
	changed = a
	changed.AppID = uuid.NewString()
	if _, err = Merge(map[string]Bundle{a.DeploymentID: a}, changed); err == nil {
		t.Fatal("app rebound")
	}
	changed = a
	changed.Files = []api.ExecutionFile{{Path: "validator.mjs", Content: []byte("tampered")}}
	if _, err = Merge(nil, changed); err == nil {
		t.Fatal("tampered bundle accepted")
	}
}
