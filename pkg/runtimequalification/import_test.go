package runtimequalification

// adr: 686

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type importStore struct {
	*state.MemStore
	records                int
	appEdit                func(*state.App)
	depEdit                func(*state.Deployment)
	releaseEdit            func(*state.RuntimeRelease)
	bindingEdit            func(*state.RuntimeRelease)
	readError, recordError error
	beforeRecord           func()
}

func (s *importStore) RuntimeReleaseByID(ctx context.Context, id string) (state.RuntimeRelease, error) {
	r, err := s.MemStore.RuntimeReleaseByID(ctx, id)
	if s.readError != nil {
		return r, s.readError
	}
	if s.releaseEdit != nil {
		s.releaseEdit(&r)
	}
	return r, err
}
func (s *importStore) AppByID(ctx context.Context, id string) (state.App, error) {
	a, err := s.MemStore.AppByID(ctx, id)
	if s.appEdit != nil {
		s.appEdit(&a)
	}
	return a, err
}
func (s *importStore) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	d, err := s.MemStore.DeploymentByID(ctx, id)
	if s.depEdit != nil {
		s.depEdit(&d)
	}
	return d, err
}
func (s *importStore) RuntimeReleaseForArtifact(ctx context.Context, account, key string) (state.RuntimeRelease, error) {
	r, err := s.MemStore.RuntimeReleaseForArtifact(ctx, account, key)
	if s.bindingEdit != nil {
		s.bindingEdit(&r)
	}
	return r, err
}
func (s *importStore) RecordRuntimeReleaseQualification(ctx context.Context, q state.RuntimeReleaseQualification) (state.RuntimeReleaseQualification, error) {
	s.records++
	if s.beforeRecord != nil {
		s.beforeRecord()
	}
	if s.recordError != nil {
		return q, s.recordError
	}
	return s.MemStore.RecordRuntimeReleaseQualification(ctx, q)
}

func TestImportRetainsExactEvidenceBeforeImmutableReceipt(t *testing.T) {
	f := newEvidenceFixture(t)
	s := &importStore{MemStore: f.store.(*state.MemStore)}
	s.beforeRecord = func() {
		for name, want := range map[string][]byte{"report.json": f.raw, "test-metal.jsonl": f.metal, "leakcheck.log": f.leak} {
			key := EvidenceKey(f.release.ID, SHA256(f.raw), name)
			if !bytes.Equal(f.artifacts.objects[key], want) {
				t.Fatalf("receipt before retained %s", name)
			}
			reads := 0
			for _, got := range f.artifacts.gets {
				if got == key {
					reads++
				}
			}
			if reads < 2 {
				t.Fatal("receipt before evidence readback", key)
			}
		}
	}
	first, err := Import(t.Context(), s, f.artifacts, f.inputs(), f.trust)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReportSHA256 != SHA256(f.raw) || first.RecordedAt.IsZero() {
		t.Fatal("unbound receipt", first)
	}
	again, err := Import(t.Context(), s, f.artifacts, f.inputs(), f.trust)
	if err != nil || !again.RecordedAt.Equal(first.RecordedAt) || len(f.artifacts.puts) != 3 {
		t.Fatal("retry changed receipt or evidence", again, err)
	}
	if err := state.RequireRuntimeReleaseQualification(t.Context(), f.store, f.release); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RevokeRuntimeReleaseQualification(t.Context(), f.release.ID, first.ReportSHA256, strings.Repeat("f", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(t.Context(), s, f.artifacts, f.inputs(), f.trust); !errors.Is(err, state.ErrConflict) {
		t.Fatal("import revived quarantine", err)
	}
	if err := state.RequireRuntimeReleaseQualification(t.Context(), f.store, f.release); !errors.Is(err, state.ErrConflict) {
		t.Fatal("revoked import eligible", err)
	}
}

func TestImportFailureCannotRecordQualification(t *testing.T) {
	outage := errors.New("storage/database unavailable")
	for _, tc := range []struct {
		name string
		edit func(*evidenceFixture, *importStore)
	}{
		{"bad signature", func(f *evidenceFixture, _ *importStore) { f.trust.PublicKey = make([]byte, 32) }},
		{"altered log", func(f *evidenceFixture, _ *importStore) { f.metal = []byte("ok\n") }},
		{"catalogue outage", func(_ *evidenceFixture, s *importStore) { s.readError = outage }},
		{"invalid catalogue", func(_ *evidenceFixture, s *importStore) {
			s.releaseEdit = func(r *state.RuntimeRelease) { r.SourceRef = "mutable:latest" }
		}},
		{"wrong guest init", func(_ *evidenceFixture, s *importStore) {
			s.releaseEdit = func(r *state.RuntimeRelease) { r.GuestInitSHA256 = strings.Repeat("d", 64); r.ID = r.Identity() }
		}},
		{"wrong fixture layer", func(_ *evidenceFixture, s *importStore) {
			s.depEdit = func(d *state.Deployment) { d.RootfsKey = "another.ext4" }
		}},
		{"customer Dockerfile", func(_ *evidenceFixture, s *importStore) {
			s.appEdit = func(a *state.App) { a.Manifest.BuildDockerfile = "Dockerfile" }
		}},
		{"other runtime", func(_ *evidenceFixture, s *importStore) { s.appEdit = func(a *state.App) { a.Runtime = "python312" } }},
		{"ordinary app", func(_ *evidenceFixture, s *importStore) { s.appEdit = func(a *state.App) { a.Type = state.AppTypeApp } }},
		{"other account", func(_ *evidenceFixture, s *importStore) { s.appEdit = func(a *state.App) { a.AccountID = "other" } }},
		{"other base binding", func(_ *evidenceFixture, s *importStore) {
			s.bindingEdit = func(r *state.RuntimeRelease) { r.ID = strings.Repeat("a", 64) }
		}},
		{"missing base", func(f *evidenceFixture, _ *importStore) { delete(f.artifacts.objects, f.release.BaseKey()) }},
		{"corrupt base", func(f *evidenceFixture, _ *importStore) { f.artifacts.objects[f.release.BaseKey()] = []byte("wrong") }},
		{"corrupt layer", func(f *evidenceFixture, _ *importStore) {
			f.artifacts.objects[f.report.Native.LayerKey] = []byte("wrong")
		}},
		{"read outage", func(f *evidenceFixture, _ *importStore) { f.artifacts.getError = outage }},
		{"archive write outage", func(f *evidenceFixture, _ *importStore) { f.artifacts.putError = outage }},
		{"archive corrupt readback", func(f *evidenceFixture, _ *importStore) { f.artifacts.corruptWrite = true }},
		{"existing evidence corrupt", func(f *evidenceFixture, _ *importStore) {
			f.artifacts.objects[EvidenceKey(f.release.ID, SHA256(f.raw), "report.json")] = []byte("wrong")
		}},
		{"existing evidence oversized", func(f *evidenceFixture, _ *importStore) {
			f.artifacts.objects[EvidenceKey(f.release.ID, SHA256(f.raw), "report.json")] = bytes.Repeat([]byte("x"), len(f.raw)+1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEvidenceFixture(t)
			s := &importStore{MemStore: f.store.(*state.MemStore)}
			tc.edit(f, s)
			if _, err := Import(t.Context(), s, f.artifacts, f.inputs(), f.trust); err == nil {
				t.Fatal("invalid import succeeded")
			}
			if s.records != 0 {
				t.Fatal("attempted qualification write", s.records)
			}
			if _, err := f.store.RuntimeReleaseQualification(t.Context(), f.release.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("failure granted qualification", err)
			}
			if strings.HasPrefix(tc.name, "existing evidence") && len(f.artifacts.puts) != 0 {
				t.Fatal("repaired corrupt audit evidence")
			}
		})
	}
}

func TestImportCancellationAndReceiptWriteFailure(t *testing.T) {
	for _, tc := range []struct {
		name          string
		cancelEarly   bool
		cancelArchive bool
		recordFailure bool
	}{
		{name: "cancel before reads", cancelEarly: true}, {name: "cancel during archive", cancelArchive: true}, {name: "receipt store outage", recordFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEvidenceFixture(t)
			s := &importStore{MemStore: f.store.(*state.MemStore)}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancelEarly {
				cancel()
			}
			if tc.cancelArchive {
				f.artifacts.afterPut = cancel
			}
			if tc.recordFailure {
				s.recordError = errors.New("database failed")
			}
			_, err := Import(ctx, s, f.artifacts, f.inputs(), f.trust)
			if err == nil {
				t.Fatal("failure accepted")
			}
			if !tc.recordFailure && (!errors.Is(err, context.Canceled) || s.records != 0) {
				t.Fatal("cancellation reached receipt", err, s.records)
			}
			if _, err := f.store.RuntimeReleaseQualification(t.Context(), f.release.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("failed import qualified", err)
			}
		})
	}
}
