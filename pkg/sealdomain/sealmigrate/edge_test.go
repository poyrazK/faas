package sealmigrate_test

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// edgeCredentials is an in-memory set of edge credential rows.
type edgeCredentials struct {
	rows map[string]state.EdgeSealedCredential // keyed by kind|id
	// rotate makes the next write to this key lose once.
	rotate string
	writes int
}

func (f *edgeCredentials) ListEdgeSealedCredentials(_ context.Context, afterKind, afterID string, limit int) ([]state.EdgeSealedCredential, error) {
	var out []state.EdgeSealedCredential
	for _, c := range f.rows {
		if c.Kind > afterKind || (c.Kind == afterKind && c.ID > afterID) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *edgeCredentials) ResealEdgeCredential(_ context.Context, c state.EdgeSealedCredential, sealed []byte) (bool, error) {
	key := c.Kind + "|" + c.ID
	if c.Managed {
		return false, fmt.Errorf("wrote managed row %s", key)
	}
	if key == f.rotate {
		f.rotate = ""
		return false, nil
	}
	if cur, ok := f.rows[key]; !ok || !bytes.Equal(cur.Sealed, c.Sealed) {
		return false, nil
	}
	f.writes++
	cur := f.rows[key]
	cur.Sealed = sealed
	f.rows[key] = cur
	return true, nil
}

func TestMigrateEdge(t *testing.T) {
	domain, fleet, stranger := mustIdentity(t), mustIdentity(t), mustIdentity(t)
	seal := func(ns, plain string, to ...*age.X25519Identity) []byte {
		var rs []*age.X25519Recipient
		for _, id := range to {
			rs = append(rs, id.Recipient())
		}
		b, err := secretbox.SealBytesTo(rs, ns, []byte(plain), 0)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	const drainNS = "APP_LOG_DRAIN_AUTH_HEADER"
	basicNS := api.AppPublicAuthBasicSealNamespace
	row := func(kind, id string, managed bool, sealed []byte) state.EdgeSealedCredential {
		return state.EdgeSealedCredential{Kind: kind, ID: id, Sealed: sealed, Managed: managed}
	}
	f := &edgeCredentials{rows: map[string]state.EdgeSealedCredential{}}
	for _, c := range []state.EdgeSealedCredential{
		row(state.EdgeSealedCredentialBasicAuth, "a1", false, seal(basicNS, "u\np", fleet)),
		row(state.EdgeSealedCredentialBasicAuth, "a2", false, seal(basicNS, "u\np", domain, fleet)),
		row(state.EdgeSealedCredentialBasicAuth, "a3", false, seal(basicNS, "u\np", domain)),
		row(state.EdgeSealedCredentialLogDrain, "d1", false, seal(drainNS, "k: v", fleet)),
		row(state.EdgeSealedCredentialLogDrain, "d2", false, seal(drainNS, "k: v", stranger)),
		row(state.EdgeSealedCredentialLogDrain, "d3", false, seal(basicNS, "k: v", fleet)),
		row(state.EdgeSealedCredentialLogDrain, "d4", false, seal(drainNS, "k: v", fleet)),
		row(state.EdgeSealedCredentialLogDrain, "d5", true, seal(drainNS, "k: v", fleet)),
		row(state.EdgeSealedCredentialLogDestination, "s1", true, seal(drainNS, "k: v", fleet)),
		row(state.EdgeSealedCredentialLogDestination, "s2", true, seal(drainNS, "k: v", domain)),
	} {
		f.rows[c.Kind+"|"+c.ID] = c
	}
	f.rotate = state.EdgeSealedCredentialLogDrain + "|d4"

	report, err := sealmigrate.MigrateEdge(context.Background(), f, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	// Re-sealed: a1, d1, and a2, which the fleet key still opens. Current:
	// a3 and the domain-sealed destination s2. Changed: d4, rotated between
	// read and write. Failed: d2 (another key) and d3 (another namespace).
	// Skipped: the managed drain d5 and destination s1.
	want := sealmigrate.MigrationReport{Domain: sealdomain.Edge, Target: domain.Recipient().String(),
		Scanned: 10, Resealed: 3, Current: 2, Changed: 1, Failed: 2, Skipped: 2}
	if report != want {
		t.Fatalf("report = %+v, want %+v", report, want)
	}
	for _, key := range []string{state.EdgeSealedCredentialBasicAuth + "|a1", state.EdgeSealedCredentialBasicAuth + "|a2", state.EdgeSealedCredentialLogDrain + "|d1"} {
		blob := f.rows[key].Sealed
		if _, _, err := secretbox.OpenBytes(fleet, blob); err == nil {
			t.Errorf("%s still opens with the fleet identity", key)
		}
		if _, _, err := secretbox.OpenBytes(domain, blob); err != nil {
			t.Errorf("%s does not open with the domain: %v", key, err)
		}
	}
	if ns, plain, err := secretbox.OpenBytes(domain, f.rows[state.EdgeSealedCredentialBasicAuth+"|a1"].Sealed); err != nil || ns != basicNS || string(plain) != "u\np" {
		t.Errorf("basic auth re-sealed as %q %q, %v; want its namespace and value kept", ns, plain, err)
	}
	if _, _, err := secretbox.OpenBytes(fleet, f.rows[state.EdgeSealedCredentialLogDrain+"|d5"].Sealed); err != nil {
		t.Error("a managed drain was rewritten")
	}

	again, err := sealmigrate.MigrateEdge(context.Background(), f, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	if again.Resealed != 1 || again.Changed != 0 || again.Skipped != 2 {
		t.Fatalf("second run = %+v; want the rotated drain re-sealed and the managed rows still skipped", again)
	}
}
