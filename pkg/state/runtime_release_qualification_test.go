package state_test

// adr: 739

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// Synthetic evidence exercises storage/fences only; it is never native proof.
func runtimeQualificationFixture(r state.RuntimeRelease) state.RuntimeReleaseQualification {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return state.RuntimeReleaseQualification{ReleaseID: r.ID, Profile: state.RuntimeQualificationProfile, Architecture: r.Architecture,
		HostID: uuid.NewString(), KernelBootID: uuid.NewString(), SourceCommit: strings.Repeat("a", 40),
		KernelSHA256: strings.Repeat("b", 64), FirecrackerSHA256: strings.Repeat("c", 64), ReportSHA256: strings.Repeat("d", 64),
		TestMetalSHA256: strings.Repeat("e", 64), LeakcheckSHA256: strings.Repeat("f", 64),
		StartedAt: now.Add(-2 * time.Minute), CompletedAt: now.Add(-time.Minute)}
}

func TestRuntimeReleaseQualificationImmutableExactAndRevocable(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		r, err := releases.PublishRuntimeRelease(t.Context(), runtimeReleaseFixture("1"))
		if err != nil {
			t.Fatal(err)
		}
		qs := s.(state.RuntimeReleaseQualificationStore)
		if err := state.RequireRuntimeReleaseQualification(t.Context(), s, r); !errors.Is(err, state.ErrConflict) {
			t.Fatal("publication qualified a target", err)
		}
		proof := runtimeQualificationFixture(r)
		first, err := qs.RecordRuntimeReleaseQualification(t.Context(), proof)
		if err != nil || first.RecordedAt.IsZero() {
			t.Fatal(first, err)
		}
		again, err := qs.RecordRuntimeReleaseQualification(t.Context(), proof)
		if err != nil || !again.RecordedAt.Equal(first.RecordedAt) {
			t.Fatal("replaced receipt", again, err)
		}
		if err := state.RequireRuntimeReleaseQualification(t.Context(), s, r); err != nil {
			t.Fatal(err)
		}
		other, err := releases.PublishRuntimeRelease(t.Context(), runtimeReleaseFixture("2"))
		if err != nil {
			t.Fatal(err)
		}
		if err := state.RequireRuntimeReleaseQualification(t.Context(), s, other); !errors.Is(err, state.ErrConflict) {
			t.Fatal("borrowed other release proof", err)
		}
		changed := proof
		changed.ReportSHA256 = strings.Repeat("1", 64)
		if _, err := qs.RecordRuntimeReleaseQualification(t.Context(), changed); !errors.Is(err, state.ErrConflict) {
			t.Fatal("overwrote evidence", err)
		}
		if err := qs.RevokeRuntimeReleaseQualification(t.Context(), r.ID, changed.ReportSHA256, strings.Repeat("2", 64)); !errors.Is(err, state.ErrConflict) {
			t.Fatal("revoked another receipt", err)
		}
		for range 2 {
			if err := qs.RevokeRuntimeReleaseQualification(t.Context(), r.ID, proof.ReportSHA256, strings.Repeat("2", 64)); err != nil {
				t.Fatal(err)
			}
		}
		revoked, err := qs.RuntimeReleaseQualification(t.Context(), r.ID)
		if err != nil || revoked.RevokedAt.IsZero() || !revoked.RecordedAt.Equal(first.RecordedAt) {
			t.Fatal(revoked, err)
		}
		if err := state.RequireRuntimeReleaseQualification(t.Context(), s, r); !errors.Is(err, state.ErrConflict) {
			t.Fatal("revoked target allowed", err)
		}
		if _, err := qs.RecordRuntimeReleaseQualification(t.Context(), proof); !errors.Is(err, state.ErrConflict) {
			t.Fatal("retry undid revocation", err)
		}
		if err := qs.RevokeRuntimeReleaseQualification(t.Context(), r.ID, proof.ReportSHA256, strings.Repeat("3", 64)); !errors.Is(err, state.ErrConflict) {
			t.Fatal("changed revocation evidence", err)
		}
		after, err := qs.RuntimeReleaseQualification(t.Context(), r.ID)
		if err != nil || !after.RevokedAt.Equal(revoked.RevokedAt) {
			t.Fatal("revocation retry changed timestamp", after, err)
		}
	})
}

func TestRuntimeReleaseQualificationRejectsIncompleteEvidence(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		r, err := releases.PublishRuntimeRelease(t.Context(), runtimeReleaseFixture("1"))
		if err != nil {
			t.Fatal(err)
		}
		qs := s.(state.RuntimeReleaseQualificationStore)
		for _, tc := range []struct {
			name string
			edit func(*state.RuntimeReleaseQualification)
			want error
		}{
			{"scan profile", func(q *state.RuntimeReleaseQualification) { q.Profile = "scan" }, state.ErrInvalidArgument},
			{"no native host", func(q *state.RuntimeReleaseQualification) { q.HostID = uuid.Nil.String() }, state.ErrInvalidArgument},
			{"no boot identity", func(q *state.RuntimeReleaseQualification) { q.KernelBootID = "unknown" }, state.ErrInvalidArgument},
			{"no source commit", func(q *state.RuntimeReleaseQualification) { q.SourceCommit = "main" }, state.ErrInvalidArgument},
			{"no kernel", func(q *state.RuntimeReleaseQualification) { q.KernelSHA256 = "" }, state.ErrInvalidArgument},
			{"no firecracker", func(q *state.RuntimeReleaseQualification) { q.FirecrackerSHA256 = "" }, state.ErrInvalidArgument},
			{"no native tests", func(q *state.RuntimeReleaseQualification) { q.TestMetalSHA256 = "" }, state.ErrInvalidArgument},
			{"no leakcheck", func(q *state.RuntimeReleaseQualification) { q.LeakcheckSHA256 = "" }, state.ErrInvalidArgument},
			{"no report", func(q *state.RuntimeReleaseQualification) { q.ReportSHA256 = "" }, state.ErrInvalidArgument},
			{"future result", func(q *state.RuntimeReleaseQualification) { q.CompletedAt = time.Now().Add(time.Hour) }, state.ErrInvalidArgument},
			{"reversed interval", func(q *state.RuntimeReleaseQualification) { q.StartedAt = q.CompletedAt }, state.ErrInvalidArgument},
			{"caller receipt", func(q *state.RuntimeReleaseQualification) { q.RecordedAt = time.Now() }, state.ErrInvalidArgument},
			{"caller revocation", func(q *state.RuntimeReleaseQualification) { q.RevocationSHA256 = strings.Repeat("1", 64) }, state.ErrInvalidArgument},
			{"wrong architecture", func(q *state.RuntimeReleaseQualification) { q.Architecture = "arm64" }, state.ErrConflict},
			{"unpublished target", func(q *state.RuntimeReleaseQualification) { q.ReleaseID = strings.Repeat("1", 64) }, state.ErrNotFound},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := runtimeQualificationFixture(r)
				tc.edit(&q)
				if _, err := qs.RecordRuntimeReleaseQualification(t.Context(), q); !errors.Is(err, tc.want) {
					t.Fatal("accepted invalid native evidence", err)
				}
			})
		}
		if _, err := qs.RuntimeReleaseQualification(t.Context(), r.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("invalid input created receipt", err)
		}
	})
}

func TestRuntimeReleaseQualificationRecordCannotRacePastRevocation(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		r, err := releases.PublishRuntimeRelease(t.Context(), runtimeReleaseFixture("1"))
		if err != nil {
			t.Fatal(err)
		}
		qs := s.(state.RuntimeReleaseQualificationStore)
		proof := runtimeQualificationFixture(r)
		if _, err := qs.RecordRuntimeReleaseQualification(t.Context(), proof); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err := qs.RecordRuntimeReleaseQualification(t.Context(), proof)
			if err != nil && !errors.Is(err, state.ErrConflict) {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			if err := qs.RevokeRuntimeReleaseQualification(t.Context(), r.ID, proof.ReportSHA256, strings.Repeat("2", 64)); err != nil {
				t.Error(err)
			}
		}()
		close(start)
		wg.Wait()
		if err := state.RequireRuntimeReleaseQualification(t.Context(), s, r); !errors.Is(err, state.ErrConflict) {
			t.Fatal("resurrected qualification", err)
		}
	})
}

type runtimeQualificationReader struct {
	state.RuntimeReleaseQualificationStore
	proof state.RuntimeReleaseQualification
	err   error
}

func (r runtimeQualificationReader) RuntimeReleaseQualification(context.Context, string) (state.RuntimeReleaseQualification, error) {
	return r.proof, r.err
}

func TestRuntimeReleaseQualificationReadFailsClosed(t *testing.T) {
	r := runtimeReleaseFixture("1")
	proof := runtimeQualificationFixture(r)
	proof.RecordedAt = time.Now().UTC()
	outage := errors.New("qualification database unavailable")
	wrongArchitecture := proof
	wrongArchitecture.Architecture = "arm64"
	unknownProfile := proof
	unknownProfile.Profile = "runtime-upgrade-native-v2"
	missingReceipt := proof
	missingReceipt.RecordedAt = time.Time{}
	for _, tc := range []struct {
		name  string
		store any
		want  error
	}{
		{"missing store", struct{}{}, state.ErrConflict},
		{"missing record", runtimeQualificationReader{err: state.ErrNotFound}, state.ErrConflict},
		{"outage", runtimeQualificationReader{err: outage}, outage},
		{"malformed evidence", runtimeQualificationReader{}, state.ErrConflict},
		{"wrong architecture", runtimeQualificationReader{proof: wrongArchitecture}, state.ErrConflict},
		{"unknown profile", runtimeQualificationReader{proof: unknownProfile}, state.ErrConflict},
		{"unrecorded evidence", runtimeQualificationReader{proof: missingReceipt}, state.ErrConflict},
		{"valid record", runtimeQualificationReader{proof: proof}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := state.RequireRuntimeReleaseQualification(t.Context(), tc.store, r)
			if !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
		})
	}
}

func TestPgRuntimeReleaseQualificationSQLFences(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	r, err := s.PublishRuntimeRelease(t.Context(), runtimeReleaseFixture("1"))
	if err != nil {
		t.Fatal(err)
	}
	proof := runtimeQualificationFixture(r)
	if _, err := s.RecordRuntimeReleaseQualification(t.Context(), proof); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"UPDATE runtime_release_qualifications SET report_sha256=repeat('1',64) WHERE release_id=$1",
		"UPDATE runtime_release_qualifications SET profile='scan' WHERE release_id=$1",
		"UPDATE runtime_release_qualifications SET architecture='arm64' WHERE release_id=$1",
		"UPDATE runtime_release_qualifications SET recorded_at=now() WHERE release_id=$1",
		"DELETE FROM runtime_release_qualifications WHERE release_id=$1",
	} {
		if _, err := pool.Exec(t.Context(), query, r.ID); err == nil {
			t.Fatal("bypassed qualification retention", query)
		}
	}
	other, err := s.PublishRuntimeRelease(t.Context(), runtimeReleaseFixture("2"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO runtime_release_qualifications
	 (release_id,profile,architecture,host_id,kernel_boot_id,source_commit,kernel_sha256,firecracker_sha256,
	 report_sha256,test_metal_sha256,leakcheck_sha256,started_at,completed_at)
	 SELECT $2,profile,'arm64',host_id,kernel_boot_id,source_commit,kernel_sha256,firecracker_sha256,
	 report_sha256,test_metal_sha256,leakcheck_sha256,started_at,completed_at
	 FROM runtime_release_qualifications WHERE release_id=$1`, r.ID, other.ID); err == nil {
		t.Fatal("direct insertion borrowed an incompatible architecture")
	}
	if err := s.RevokeRuntimeReleaseQualification(t.Context(), r.ID, proof.ReportSHA256, strings.Repeat("2", 64)); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"UPDATE runtime_release_qualifications SET revoked_at=NULL,revocation_sha256=NULL WHERE release_id=$1",
		"UPDATE runtime_release_qualifications SET revocation_sha256=repeat('3',64) WHERE release_id=$1",
		"DELETE FROM runtime_releases WHERE id=$1",
	} {
		if _, err := pool.Exec(t.Context(), query, r.ID); err == nil {
			t.Fatal("bypassed permanent revocation", query)
		}
	}
}
