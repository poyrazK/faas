package gateway

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// legacyFreezePublicRouting is pinPublicRoutingPolicy's copy and proof as they
// were before snapshots were shared. The shared path must produce the same
// copy and byte-for-byte the same proof: it is the request's effective policy
// revision, reported as evidence.
func legacyFreezePublicRouting(source PublicRoutingSnapshot, base string) (PublicRoutingSnapshot, string, error) {
	encoded, err := json.Marshal(source)
	if err != nil {
		return PublicRoutingSnapshot{}, "", err
	}
	var frozen PublicRoutingSnapshot
	if err := json.Unmarshal(encoded, &frozen); err != nil {
		return PublicRoutingSnapshot{}, "", err
	}
	proof, err := json.Marshal(struct {
		Base    string
		Routing json.RawMessage
	}{base, encoded})
	if err != nil {
		return PublicRoutingSnapshot{}, "", err
	}
	return frozen, policyDigest(proof), nil
}

func sharedFreezePublicRouting(c *publicRoutingFreezes, source PublicRoutingSnapshot, base string) (PublicRoutingSnapshot, string, error) {
	encoded, err := json.Marshal(source)
	if err != nil {
		return PublicRoutingSnapshot{}, "", err
	}
	sealed, err := c.seal(encoded)
	if err != nil {
		return PublicRoutingSnapshot{}, "", err
	}
	proof, err := sealed.proof(base)
	return sealed.frozen, proof, err
}

func publicRoutingFreezeSources() map[string]PublicRoutingSnapshot {
	return map[string]PublicRoutingSnapshot{
		"empty":   {},
		"weights": {AppID: "app", AccountID: "owner", Scope: "production", Weights: []DeploymentWeightsRow{{ID: "d1", TrafficPercent: 90}, {ID: "d2", TrafficPercent: 10}}},
		"release": {AppID: "app", AccountID: "owner", Scope: "production", ReleaseRequested: "r1", ReleaseID: "r1", ReleaseDeploymentID: "d1",
			ReleaseVerdict: "allowed", ReleaseResolved: true, RosterScope: "default"},
		"revision and host": {AppID: "app", AccountID: "owner", RevisionID: "rev", RevisionChecked: true, RevisionAllowed: true,
			HostDeploymentID: "d3", HostScope: "preview", HostChecked: true, HostAllowed: true, Async: true, Weights: []DeploymentWeightsRow{}},
		"escaped":                  {AppID: "app<&>", AccountID: "ünïcode", Scope: "  \"q\""},
		"selection is not encoded": {AppID: "app", SelectedDeploymentID: "d9", SelectionReason: "host"},
	}
}

func TestPublicRoutingFreezesMatchTheUnsharedCopy(t *testing.T) {
	var freezes publicRoutingFreezes
	for name, source := range publicRoutingFreezeSources() {
		for _, base := range []string{"", "traffic-v1:abc", "traffic-v1:<&>"} {
			wantFrozen, wantProof, err := legacyFreezePublicRouting(source, base)
			if err != nil {
				t.Fatal(err)
			}
			for _, pass := range []string{"miss", "hit"} {
				gotFrozen, gotProof, err := sharedFreezePublicRouting(&freezes, source, base)
				if err != nil {
					t.Fatalf("%s/%q/%s: %v", name, base, pass, err)
				}
				if gotProof != wantProof || !reflect.DeepEqual(gotFrozen, wantFrozen) {
					t.Fatalf("%s/%q/%s: shared freeze diverged: %s %+v, want %s %+v", name, base, pass, gotProof, gotFrozen, wantProof, wantFrozen)
				}
			}
		}
	}
}

// A request's selection writes its own copy, never the shared snapshot.
func TestPublicRoutingFreezeCopiesAreIndependent(t *testing.T) {
	var freezes publicRoutingFreezes
	source := publicRoutingFreezeSources()["weights"]
	first, _, err := sharedFreezePublicRouting(&freezes, source, "base")
	if err != nil {
		t.Fatal(err)
	}
	first.SelectedDeploymentID, first.SelectionReason = "d1", "weighted"
	second, _, err := sharedFreezePublicRouting(&freezes, source, "base")
	if err != nil {
		t.Fatal(err)
	}
	if second.SelectedDeploymentID != "" || second.SelectionReason != "" {
		t.Fatalf("a request's selection leaked into the shared snapshot: %+v", second)
	}
}

// Proofs stay right past the memo's capacity and for bases too long to memoize.
func TestPublicRoutingFreezeProofsFollowTheBase(t *testing.T) {
	var freezes publicRoutingFreezes
	source := publicRoutingFreezeSources()["weights"]
	seen := map[string]bool{}
	for pass := range 3 {
		for i := range publicRoutingProofMemoEntries + 4 {
			base := fmt.Sprintf("traffic-v1:%d", i)
			if i == 0 {
				base = string(make([]byte, publicRoutingProofMemoKeyBytes+1))
			}
			_, want, err := legacyFreezePublicRouting(source, base)
			if err != nil {
				t.Fatal(err)
			}
			_, got, err := sharedFreezePublicRouting(&freezes, source, base)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("pass %d base %d: proof %s, want %s", pass, i, got, want)
			}
			if pass == 0 {
				if seen[got] {
					t.Fatalf("base %d shares proof %s with another base", i, got)
				}
				seen[got] = true
			}
		}
	}
}

func TestPublicRoutingFreezesAreSafeForConcurrentRequests(t *testing.T) {
	var freezes publicRoutingFreezes
	sources := publicRoutingFreezeSources()
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Go(func() {
			for i := range 200 {
				for name, source := range sources {
					base := fmt.Sprintf("traffic-v1:%d", (worker+i)%3)
					_, want, err := legacyFreezePublicRouting(source, base)
					if err != nil {
						t.Error(err)
						return
					}
					frozen, got, err := sharedFreezePublicRouting(&freezes, source, base)
					if err != nil || got != want {
						t.Errorf("%s: proof %s err %v, want %s", name, got, err, want)
						return
					}
					frozen.SelectedDeploymentID = name
				}
			}
		})
	}
	wg.Wait()
}

func BenchmarkPublicRoutingFreeze(b *testing.B) {
	source := publicRoutingFreezeSources()["weights"]
	b.Run("unshared", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := legacyFreezePublicRouting(source, "traffic-v1:abc"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("shared", func(b *testing.B) {
		var freezes publicRoutingFreezes
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := sharedFreezePublicRouting(&freezes, source, "traffic-v1:abc"); err != nil {
				b.Fatal(err)
			}
		}
	})
}
