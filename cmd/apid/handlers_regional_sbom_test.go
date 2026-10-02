package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type sbomReferenceStore struct {
	state.Store
	reference string
}

func (s sbomReferenceStore) BuildProvenanceByBuildID(ctx context.Context, id string) (state.BuildProvenance, error) {
	p, err := s.Store.BuildProvenanceByBuildID(ctx, id)
	p.SBOMStorageKey = s.reference // Simulate historical or corrupted persisted metadata.
	return p, err
}

type sbomReadBackend struct {
	storage.StorageBackend
	gets        int
	unavailable bool
}

func (s *sbomReadBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	s.gets++
	if s.unavailable {
		return nil, errors.New("archive unavailable")
	}
	return s.StorageBackend.Get(ctx, key)
}

func TestGetRegionalBuildSBOM(t *testing.T) {
	for _, scenario := range []string{"success", "missing object", "missing route", "unavailable", "wrong region", "wrong build", "global key", "alias", "other artifact", "unrouted backend", "local fallback", "foreign account"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			_, auth, store, acct := sbomTestServer(t, root)
			id := "eu-sbom"
			if _, err := store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: true}); err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "regional-sbom", Type: state.AppTypeFunction, Runtime: "node22", RAMMB: 256, ComputeRegion: id})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball})
			if err != nil {
				t.Fatal(err)
			}
			build, err := store.CreateBuild(t.Context(), dep.ID, dep.Kind, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			key := region.BuildSBOMKey(id, build.ID)
			if err := store.CreateBuildProvenance(t.Context(), state.BuildProvenance{BuildID: build.ID, StartedAt: time.Now(), FinishedAt: time.Now(), SBOMStorageKey: key}); err != nil {
				t.Fatal(err)
			}
			legacy, _ := storage.NewLocalStorageBackend(root)
			eu, _ := storage.NewLocalStorageBackend(t.TempDir())
			payload := `{"bomFormat":"CycloneDX","specVersion":"1.5","version":1,"components":[]}`
			// Trap both global fallback and a local path that looks regional.
			for _, k := range []string{key, region.BuildSBOMKey("", build.ID)} {
				if err := legacy.Put(t.Context(), k, strings.NewReader("wrong storage")); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "missing object" {
				if err := eu.Put(t.Context(), region.BuildSBOMKey("", build.ID), strings.NewReader(payload)); err != nil {
					t.Fatal(err)
				}
			}
			observed := &sbomReadBackend{StorageBackend: eu, unavailable: scenario == "unavailable"}
			routes := map[string]storage.StorageBackend{id: observed}
			if scenario == "missing route" {
				routes = nil
			}
			router, err := storage.NewRegionRouter(routes, legacy)
			if err != nil {
				t.Fatal(err)
			}
			ref := key
			switch scenario {
			case "wrong region":
				ref = region.BuildSBOMKey("us-sbom", build.ID)
			case "wrong build":
				ref = region.BuildSBOMKey(id, uuid.NewString())
			case "global key":
				ref = region.BuildSBOMKey("", build.ID)
			case "alias":
				ref = "regions/" + id + "/sboms/../sboms/" + build.ID + ".cdx.json"
			case "other artifact":
				ref = region.ArtifactKey(id, "sources/"+build.ID+".tar.gz")
			}
			srv := newServer(sbomReferenceStore{Store: store, reference: ref}, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithSBOMStorage(router).WithSBOMRoot(root)
			if scenario == "unrouted backend" {
				srv.WithSBOMStorage(legacy)
			}
			if scenario == "local fallback" {
				srv.WithSBOMStorage(nil)
			}
			if scenario == "foreign account" {
				other, err := store.CreateAccount(t.Context(), "other-sbom@example.com", api.PlanHobby)
				if err != nil {
					t.Fatal(err)
				}
				plain, hash, err := api.GenerateAPIKey()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.CreateAPIKey(t.Context(), other.ID, hash, "other", api.ScopesAdminOnly); err != nil {
					t.Fatal(err)
				}
				auth = plain
			}
			resp := sbomGet(t, srv.handler(), auth, "/v1/builds/"+build.ID+"/sbom")
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "success" {
				if resp.StatusCode != http.StatusOK || string(body) != payload || resp.Header.Get("Content-Type") != "application/vnd.cyclonedx+json" {
					t.Fatalf("status=%d body=%s", resp.StatusCode, body)
				}
			} else if scenario == "foreign account" {
				if resp.StatusCode != http.StatusNotFound {
					t.Fatalf("status=%d body=%s", resp.StatusCode, body)
				}
			} else if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", resp.StatusCode, body)
			}
			wantGets := 0
			if scenario == "success" || scenario == "missing object" || scenario == "unavailable" {
				wantGets = 1
			}
			if observed.gets != wantGets {
				t.Fatalf("regional backend read %d times, want %d", observed.gets, wantGets)
			}
		})
	}
}
