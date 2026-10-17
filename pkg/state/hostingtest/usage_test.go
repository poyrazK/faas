package hostingtest

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func sizedManifest(size int64) api.HostingManifest {
	files := []api.HostingFile{}
	for size > 0 {
		n := min(size, api.HostingFileMaxBytes)
		name := "index.html"
		if len(files) > 0 {
			name = fmt.Sprintf("asset-%d.js", len(files))
		}
		files = append(files, api.HostingFile{Path: name, Size: n, SHA256: strings.Repeat("a", 64), ContentType: "text/html"})
		size -= n
	}
	return api.HostingManifest{Files: files}
}

func accountStorageUsage(t *testing.T, store hostingTestStore) {
	account, site := seed(t, store)
	other, _ := seed(t, store)
	input := site
	input.ID = uuid.NewString()
	input.Name = "second"
	input.Hostname = input.ID + ".hosting.example"
	second, err := store.CreateHostingSite(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	payload := api.HostingReleaseMaxBytes
	reservation := api.HostingStorageCopies*payload + api.HostingManifestMaxBytes
	retained := api.HostingAccountStorageMaxBytes / reservation
	releases := []api.HostingRelease{}
	for range retained {
		release, err := store.CreateHostingRelease(t.Context(), account.ID, site.ID, sizedManifest(payload))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.ReadyHostingRelease(t.Context(), account.ID, site.ID, release.ID); err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	usage, err := store.GetHostingUsage(t.Context(), account.ID)
	if err != nil || usage.ReservedBytes != retained*reservation || usage.ReadyPayloadBytes != retained*payload || usage.PendingUploads != 0 {
		t.Fatalf("ready usage: %+v %v", usage, err)
	}
	foreign, _ := store.GetHostingUsage(t.Context(), other.ID)
	if foreign.ReservedBytes != 0 {
		t.Fatal("usage leaked across accounts")
	}
	// Promotion and rollback select retained bytes rather than allocating more.
	for _, index := range []int{0, 1, 0} {
		current, err := store.GetHostingSite(t.Context(), account.ID, site.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.PromoteHostingRelease(t.Context(), account.ID, site.ID, releases[index].ID, current.Revision); err != nil {
			t.Fatal(err)
		}
	}
	again, _ := store.GetHostingUsage(t.Context(), account.ID)
	if again.ReservedBytes != usage.ReservedBytes {
		t.Fatal("rollback double-counted storage")
	}
	// Across two different site locks, only one final reservation fits.
	remaining := api.HostingAccountStorageMaxBytes - usage.ReservedBytes
	small := (remaining - api.HostingManifestMaxBytes) / api.HostingStorageCopies
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, target := range []api.HostingSite{site, second} {
		wg.Go(func() {
			_, err := store.CreateHostingRelease(t.Context(), account.ID, target.ID, sizedManifest(small))
			results <- err
		})
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
			continue
		}
		var quota *state.HostingQuotaError
		if !errors.As(err, &quota) || quota.Resource != "storage_bytes" || quota.Limit != api.HostingAccountStorageMaxBytes || quota.Observed <= quota.Limit {
			t.Fatalf("storage refusal: %v", err)
		}
	}
	if accepted != 1 {
		t.Fatalf("cross-site over-admission: %d", accepted)
	}
	before, _ := store.GetHostingUsage(t.Context(), account.ID)
	retiring, err := store.RetireHostingRelease(t.Context(), account.ID, site.ID, releases[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := store.GetHostingUsage(t.Context(), account.ID)
	if after.ReservedBytes != before.ReservedBytes || after.RetiringReservedBytes != reservation {
		t.Fatalf("retirement freed capacity early: %+v", after)
	}
	if err := store.DeleteHostingReleaseRecord(t.Context(), account.ID, site.ID, retiring.ID); err != nil {
		t.Fatal(err)
	}
	cleaned, _ := store.GetHostingUsage(t.Context(), account.ID)
	if cleaned.ReservedBytes != before.ReservedBytes-reservation || cleaned.RetiringReservedBytes != 0 {
		t.Fatalf("cleanup reservation not released: %+v", cleaned)
	}
	// Retiring/deleting the remaining site catalog must retain its reservations.
	if _, err := store.DeleteHostingSite(t.Context(), account.ID, site.ID); err != nil {
		t.Fatal(err)
	}
	deleting, _ := store.GetHostingUsage(t.Context(), account.ID)
	if deleting.ReservedBytes != cleaned.ReservedBytes {
		t.Fatal("site deletion released capacity before cleanup")
	}
}

func pendingUploadUsage(t *testing.T, store hostingTestStore) {
	account, site := seed(t, store)
	input := site
	input.ID = uuid.NewString()
	input.Name = "second"
	input.Hostname = input.ID + ".hosting.example"
	second, err := store.CreateHostingSite(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	var first api.HostingRelease
	for i := int64(0); i < api.HostingAccountPendingUploadsMax; i++ {
		target := site
		if i%2 == 1 {
			target = second
		}
		release, err := store.CreateHostingRelease(t.Context(), account.ID, target.ID, manifest())
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = release
		}
	}
	if _, err := store.CreateHostingRelease(t.Context(), account.ID, second.ID, manifest()); err == nil {
		t.Fatal("pending cap was per site")
	} else {
		var quota *state.HostingQuotaError
		if !errors.As(err, &quota) || quota.Resource != "pending_uploads" || quota.Observed != api.HostingAccountPendingUploadsMax+1 {
			t.Fatalf("pending quota detail: %v", err)
		}
	}
	before, _ := store.GetHostingUsage(t.Context(), account.ID)
	if _, err := store.ReadyHostingRelease(t.Context(), account.ID, site.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := store.GetHostingUsage(t.Context(), account.ID)
	if after.ReservedBytes != before.ReservedBytes || after.PendingUploads != before.PendingUploads-1 {
		t.Fatal("finalize released bytes or retained pending slot")
	}
	if _, err := store.CreateHostingRelease(t.Context(), account.ID, second.ID, manifest()); err != nil {
		t.Fatal("pending capacity not restored", err)
	}
	// The original per-site retained-release cap still applies to ready releases.
	another, third := seed(t, store)
	for range api.HostingReleasesPerSite {
		release, err := store.CreateHostingRelease(t.Context(), another.ID, third.ID, manifest())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.ReadyHostingRelease(t.Context(), another.ID, third.ID, release.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateHostingRelease(t.Context(), another.ID, third.ID, manifest()); !errors.Is(err, state.ErrHostingQuota) {
		t.Fatalf("retained-release cap: %v", err)
	}
}
