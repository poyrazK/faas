package state

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// RuntimeQualificationProfile names the trusted native acceptance contract:
// dedicated Linux hardware with KVM (no nested virtualization), exact release
// bytes, artifact cold boot/readiness, test-metal, and a successful leakcheck.
// A scan, unit test or environment qualification cannot satisfy this profile.
const RuntimeQualificationProfile = "runtime-upgrade-native-v1"

// RuntimeReleaseQualification retains identities and digests of operator-
// verified native evidence. The profile is an assertion by the trusted native
// acceptance owner, not a verdict derived from customer input or log hashes.
// Qualification is separate from each candidate's fresh readiness and cutover.
type RuntimeReleaseQualification struct {
	ReleaseID, Profile, Architecture, HostID, KernelBootID, SourceCommit string
	KernelSHA256, FirecrackerSHA256, ReportSHA256                        string
	TestMetalSHA256, LeakcheckSHA256                                     string
	StartedAt, CompletedAt, RecordedAt                                   time.Time
	RevokedAt                                                            time.Time
	RevocationSHA256                                                     string
}

// RuntimeReleaseQualificationReader grants preparation workers no write authority.
type RuntimeReleaseQualificationReader interface {
	RuntimeReleaseQualification(context.Context, string) (RuntimeReleaseQualification, error)
}

// RuntimeReleaseQualificationStore is private operator infrastructure. No
// customer API may record evidence. The future native acceptance importer must
// verify the reports, physical host and exact published bytes before recording.
// Revocation is permanent for this receipt/release; retries cannot undo it.
type RuntimeReleaseQualificationStore interface {
	RuntimeReleaseQualificationReader
	RecordRuntimeReleaseQualification(context.Context, RuntimeReleaseQualification) (RuntimeReleaseQualification, error)
	RevokeRuntimeReleaseQualification(context.Context, string, string, string) error
}

var runtimeQualificationCommit = regexp.MustCompile(`^[a-f0-9]{40}$|^[a-f0-9]{64}$`)

func (q RuntimeReleaseQualification) validateEvidence(now time.Time) error {
	if q.Profile != RuntimeQualificationProfile || (q.Architecture != "amd64" && q.Architecture != "arm64") ||
		!runtimeQualificationCommit.MatchString(q.SourceCommit) || q.StartedAt.IsZero() ||
		!q.CompletedAt.After(q.StartedAt) || q.CompletedAt.After(now) {
		return ErrInvalidArgument
	}
	for _, value := range []string{q.ReleaseID, q.KernelSHA256, q.FirecrackerSHA256, q.ReportSHA256, q.TestMetalSHA256, q.LeakcheckSHA256} {
		if !runtimeSHA.MatchString(value) {
			return ErrInvalidArgument
		}
	}
	for _, value := range []string{q.HostID, q.KernelBootID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return ErrInvalidArgument
		}
	}
	return nil
}

func sameRuntimeQualificationEvidence(a, b RuntimeReleaseQualification) bool {
	return a.ReleaseID == b.ReleaseID && a.Profile == b.Profile && a.Architecture == b.Architecture &&
		a.HostID == b.HostID && a.KernelBootID == b.KernelBootID && a.SourceCommit == b.SourceCommit &&
		a.KernelSHA256 == b.KernelSHA256 && a.FirecrackerSHA256 == b.FirecrackerSHA256 && a.ReportSHA256 == b.ReportSHA256 &&
		a.TestMetalSHA256 == b.TestMetalSHA256 && a.LeakcheckSHA256 == b.LeakcheckSHA256 &&
		a.StartedAt.Equal(b.StartedAt) && a.CompletedAt.Equal(b.CompletedAt)
}

func normalizeRuntimeQualificationInput(q RuntimeReleaseQualification) (RuntimeReleaseQualification, error) {
	q.StartedAt = q.StartedAt.UTC().Truncate(time.Microsecond)
	q.CompletedAt = q.CompletedAt.UTC().Truncate(time.Microsecond)
	if q.validateEvidence(time.Now().UTC()) != nil || !q.RecordedAt.IsZero() || !q.RevokedAt.IsZero() || q.RevocationSHA256 != "" {
		return RuntimeReleaseQualification{}, ErrInvalidArgument
	}
	return q, nil
}

// RequireRuntimeReleaseQualification fails closed for explicit upgrade targets.
// This is a point-in-time preparation check, never long-lived activation
// authority. The eventual cutover must serialize its own check with revocation.
func RequireRuntimeReleaseQualification(ctx context.Context, store any, target RuntimeRelease) error {
	qualified, ok := store.(RuntimeReleaseQualificationReader)
	if !ok {
		return fmt.Errorf("runtime update qualification store is unavailable: %w", ErrConflict)
	}
	q, err := qualified.RuntimeReleaseQualification(ctx, target.ID)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("runtime update target lacks native qualification: %w", ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("read runtime update qualification: %w", err)
	}
	if target.Validate() != nil || q.validateEvidence(time.Now().UTC()) != nil || q.ReleaseID != target.ID ||
		q.Architecture != target.Architecture || q.RecordedAt.IsZero() || q.RecordedAt.Before(q.CompletedAt) ||
		q.RecordedAt.After(time.Now().UTC()) || !q.RevokedAt.IsZero() || q.RevocationSHA256 != "" {
		return fmt.Errorf("runtime update native qualification is incompatible or revoked: %w", ErrConflict)
	}
	return nil
}

var _ RuntimeReleaseQualificationStore = (*MemStore)(nil)

func (m *MemStore) RecordRuntimeReleaseQualification(_ context.Context, q RuntimeReleaseQualification) (RuntimeReleaseQualification, error) {
	q, err := normalizeRuntimeQualificationInput(q)
	if err != nil {
		return RuntimeReleaseQualification{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runtimeReleases[q.ReleaseID]
	if !ok {
		return RuntimeReleaseQualification{}, ErrNotFound
	}
	if r.Architecture != q.Architecture {
		return RuntimeReleaseQualification{}, ErrConflict
	}
	if old, ok := m.runtimeReleaseQualifications[q.ReleaseID]; ok {
		if !sameRuntimeQualificationEvidence(old, q) || !old.RevokedAt.IsZero() {
			return RuntimeReleaseQualification{}, ErrConflict
		}
		return old, nil
	}
	if m.runtimeReleaseQualifications == nil {
		m.runtimeReleaseQualifications = make(map[string]RuntimeReleaseQualification)
	}
	q.RecordedAt = time.Now().UTC()
	m.runtimeReleaseQualifications[q.ReleaseID] = q
	return q, nil
}

func (m *MemStore) RuntimeReleaseQualification(_ context.Context, id string) (RuntimeReleaseQualification, error) {
	if !runtimeSHA.MatchString(id) {
		return RuntimeReleaseQualification{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	q, ok := m.runtimeReleaseQualifications[id]
	if !ok {
		return RuntimeReleaseQualification{}, ErrNotFound
	}
	return q, nil
}

// expectedReport fences revocation to the operator-reviewed receipt. reason is
// the SHA-256 of a retained revocation report, rather than free-form customer text.
func (m *MemStore) RevokeRuntimeReleaseQualification(_ context.Context, id, expectedReport, reason string) error {
	if !runtimeSHA.MatchString(id) || !runtimeSHA.MatchString(expectedReport) || !runtimeSHA.MatchString(reason) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	q, ok := m.runtimeReleaseQualifications[id]
	if !ok {
		return ErrNotFound
	}
	if q.ReportSHA256 != expectedReport || !q.RevokedAt.IsZero() && q.RevocationSHA256 != reason {
		return ErrConflict
	}
	if q.RevokedAt.IsZero() {
		q.RevokedAt, q.RevocationSHA256 = time.Now().UTC(), reason
		m.runtimeReleaseQualifications[id] = q
	}
	return nil
}
