package imaged

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// production-us, rc.312 rollout (2026-10-31): fsn-2 served rc.312 and fsn-3
// rc.311. Each release has its own guest-init, so each rebuilt the other's
// runner-base-debian-parent on its next stage and republished it as current:
// ten swaps in 75 minutes, and every swap failed the scans and snapshot boots
// in flight, including fsn-3's pre-stage. A serving imaged keeps a producer
// another release published after it started; the release pre-stage still
// ships its own guest-init.
func TestVerifiedBaseServingReleaseKeepsANewerReleasesGuestInit(t *testing.T) {
	cases := []struct {
		name string
		// startedAfterPublish: the other imaged started after the newer
		// release published (a rollback or a restart), not before.
		startedAfterPublish bool
		prestage            bool
		wantKept            bool
	}{
		{name: "older release serving through the rollout keeps it", wantKept: true},
		{name: "release started after the publish rebuilds", startedAfterPublish: true},
		{name: "release pre-stage ships its own guest-init", prestage: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newer, _, puller, _, ref := verifiedBaseFixture(t, "complete")
			be, err := newer.storageFor()
			if err != nil {
				t.Fatal(err)
			}
			olderInit := filepath.Join(t.TempDir(), "guest-init")
			if err := os.WriteFile(olderInit, []byte("OLDER RELEASE INIT"), 0o755); err != nil {
				t.Fatal(err)
			}
			older := New(newer.store, &fakeNotifier{}, puller, newer.builder, olderInit, newer.appsRoot, silentLogger()).
				WithStorage(be).WithReleasePrestage(tc.prestage)
			older.servingSince = time.Now().UTC().Add(-time.Hour)

			const baseKey, digestKey = "base/verified.ext4", "base/verified.digest"
			if _, err := newer.EnsureBaseExt4(t.Context(), ref, baseKey, digestKey, "", "", ""); err != nil {
				t.Fatalf("newer release stages the base: %v", err)
			}
			store := newer.store.(state.BaseImageProducerStore)
			published, err := store.GetCurrentBaseImageProducer(t.Context(), baseKey)
			if err != nil {
				t.Fatal(err)
			}
			if tc.startedAfterPublish {
				older.servingSince = published.PublishedAt.Add(time.Second)
			}

			if _, err := older.EnsureBaseExt4(t.Context(), ref, baseKey, digestKey, "", "", ""); err != nil {
				t.Fatalf("older release stages the base: %v", err)
			}
			current, err := store.GetCurrentBaseImageProducer(t.Context(), baseKey)
			if err != nil {
				t.Fatal(err)
			}
			if kept := current.ID == published.ID; kept != tc.wantKept {
				t.Fatalf("newer release's producer kept = %v, want %v (current guest-init %s, newer %s)",
					kept, tc.wantKept, current.Input.GuestInitDigest, published.Input.GuestInitDigest)
			}
			olderDigest, err := guestInitBinaryDigest(olderInit)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.wantKept && current.Input.GuestInitDigest != baseGuestDigest(olderDigest) {
				t.Fatalf("rebuilt producer guest-init = %s, want the older release's %s", current.Input.GuestInitDigest, olderDigest)
			}
			// The digest sidecar names the guest-init built into the bytes,
			// not the one of whichever imaged staged them last.
			sidecar := string(readStored(t, be, digestKey))
			want := "guest-init-sha256=" + strings.TrimPrefix(current.Input.GuestInitDigest, "sha256:")
			if !strings.Contains(sidecar, want) {
				t.Fatalf("digest sidecar %q does not name the base's guest-init %s", sidecar, want)
			}
		})
	}
}
