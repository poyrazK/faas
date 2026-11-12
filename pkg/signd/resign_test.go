package signd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/signd"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

// ADR-20261109170000 §5 step 3: the re-sign pass turns every valid v1
// signature into a v2 one, records app layers, leaves anything it cannot
// prove alone, writes nothing in a dry run, and finds its own work current
// on a rerun.
func TestResign(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	local, err := cosign.NewLocalSigner(f.privPath, f.objects, nil)
	if err != nil {
		t.Fatal(err)
	}
	const otherDep = "22222222-2222-2222-2222-222222222222"
	var (
		app      = storagekey.AppLayer("alice", depID)
		sidecar  = storagekey.AppSidecarLayer("alice", depID, "proxy")
		legacy   = storagekey.Layer(otherDep)
		tampered = storagekey.AppLayer("bob", otherDep)
		unsigned = storagekey.AppLayer("carol", otherDep)
		v2Signed = storagekey.AppLayer("dave", otherDep)
	)
	for _, k := range []string{app, sidecar, legacy, tampered, unsigned, v2Signed} {
		f.objects.blobs[k] = bytes.Repeat([]byte(k), 64)
	}
	for _, k := range []string{app, sidecar, legacy, tampered} {
		if err := local.Sign(ctx, k, cosign.SigKeyFor(k)); err != nil {
			t.Fatal(err)
		}
	}
	f.objects.blobs[tampered] = append(f.objects.blobs[tampered], 'x')
	if err := local.WithSignatureV2(true).Sign(ctx, v2Signed, cosign.SigKeyFor(v2Signed)); err != nil {
		t.Fatal(err)
	}
	f.store.layers = []signd.StoredLayer{{depID, app}, {depID, sidecar}, {otherDep, legacy}, {otherDep, tampered}, {otherDep, unsigned}, {otherDep, v2Signed}}

	before := snapshot(f.objects)
	dry, err := signd.Resign(ctx, f.store, f.objects, f.priv, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Converted != 3 || dry.Current != 1 || dry.Recorded != 0 || len(dry.Problems) != 2 {
		t.Fatalf("dry run = %+v", dry)
	}
	if !bytes.Equal(snapshot(f.objects), before) || len(f.store.rows) != 0 {
		t.Fatal("the dry run wrote")
	}

	got, err := signd.Resign(ctx, f.store, f.objects, f.priv, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Converted != 3 || got.Current != 1 || got.Recorded != 3 || len(got.Problems) != 2 {
		t.Fatalf("resign = %+v", got)
	}
	for _, k := range []string{app, sidecar, legacy, v2Signed} {
		var doc cosign.SignatureV2
		if err := json.Unmarshal(f.objects.blobs[cosign.SigKeyFor(k)], &doc); err != nil || doc.Key != k {
			t.Errorf("%s: signature is not a v2 document for its key: %v", k, err)
		}
		if err := f.verify(t, k); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
	if r := f.store.rows[app]; r.NodeName != signd.ResignNode || r.DeploymentID != depID || r.Format != "v2" {
		t.Errorf("app row = %+v", r)
	}
	if _, ok := f.store.rows[legacy]; ok {
		t.Error("a legacy layers/ key got an artifact_signatures row")
	}
	if _, ok := f.store.rows[tampered]; ok {
		t.Error("a layer whose signature fails got a row")
	}
	if len(f.objects.blobs[cosign.SigKeyFor(tampered)]) != 64 {
		t.Error("the pass rewrote a signature it could not verify")
	}

	again, err := signd.Resign(ctx, f.store, f.objects, f.priv, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again.Converted != 0 || again.Current != 4 || again.Recorded != 0 || len(again.Problems) != 2 {
		t.Fatalf("rerun = %+v", again)
	}
}

func snapshot(m *memObjects) []byte {
	b, _ := json.Marshal(m.blobs)
	return b
}
