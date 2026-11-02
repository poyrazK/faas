package ci

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/credhash"
)

// adr: 20261029202403 amendment 1 decision 3 — the CI runtime capability is
// an HMAC under the scheduler key once its keyed writes are on, so the lease
// token the database holds no longer yields it; the old derivation is
// accepted only while plain lookup is on.
func TestRuntimeCapabilityIsDerivedWithTheSchedulerKey(t *testing.T) {
	line, err := credhash.NewKeyLine(1)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := credhash.ParseRing([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { credhash.Set(credhash.FamilyScheduler, nil) })
	const run, instance, lease = "run-1", "instance-1", "lease-1"
	legacy := legacyRuntimeCapability(run, instance, lease)

	credhash.Set(credhash.FamilyScheduler, nil)
	if got := runtimeCapability(run, instance, lease); got != legacy || !runtimeCapabilityMatches(run, instance, lease, legacy) {
		t.Fatal("the release before must issue and accept the old capability")
	}
	if !ValidRuntimeProof(RuntimeProof{RunID: "00000000-0000-0000-0000-000000000001", InstanceID: "00000000-0000-0000-0000-000000000002", Capability: legacy}) {
		t.Fatal("old capability shape refused")
	}

	credhash.Set(credhash.FamilyScheduler, &credhash.Policy{Ring: ring, KeyedWrites: true, PlainLookup: true})
	keyed := runtimeCapability(run, instance, lease)
	if keyed == legacy || !ValidRuntimeProof(RuntimeProof{RunID: "00000000-0000-0000-0000-000000000001", InstanceID: "00000000-0000-0000-0000-000000000002", Capability: keyed}) {
		t.Fatalf("keyed capability %q is not a valid proof", keyed)
	}
	if !runtimeCapabilityMatches(run, instance, lease, keyed) || !runtimeCapabilityMatches(run, instance, lease, legacy) {
		t.Fatal("the rollout refuses a capability it issues")
	}
	stored := storedRuntimeHash(keyed)
	if !credhash.Keyed(stored) || !runtimeHashMatches(stored, keyed) || runtimeHashMatches(stored, legacy) {
		t.Fatalf("stored hash %x does not bind the keyed capability alone", stored)
	}

	credhash.Set(credhash.FamilyScheduler, &credhash.Policy{Ring: ring, KeyedWrites: true})
	if runtimeCapabilityMatches(run, instance, lease, legacy) {
		t.Fatal("contract step still accepts a capability derived from the lease token alone")
	}
	if !runtimeCapabilityMatches(run, instance, lease, keyed) || runtimeCapabilityMatches(run, instance, "other-lease", keyed) {
		t.Fatal("keyed capability not bound to its lease")
	}
}
