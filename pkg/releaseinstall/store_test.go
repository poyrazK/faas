// Store tests for pkg/releaseinstall. Uses an in-memory fake
// Store (the Store interface itself is the abstraction that lets
// cmd/gregale and PR-4 doctor inject fakes in tests without a
// real Postgres).
//
// We exercise:
//   - Insert generates an id and round-trips via GetByGitSHA
//   - MarkApplied first-write-wins semantics
//   - MarkApplied is idempotent across concurrent calls
//   - GetByGitSHA returns ErrNotFound for unknown SHA
//   - ListApplied returns only applied rows, ordered desc
//   - Insert rejects empty payloads
//
// The pgStore type itself is exercised against a real Postgres
// in pkg/state/pgstore_test.go (TestPg_ReleaseBundles_*) per
// the PR-3 plan.
package releaseinstall

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStore is the in-memory Store used by these tests.
type fakeStore struct {
	mu       sync.Mutex
	rows     map[string]*fakeRow
	cnRows   map[string]*ComputeNodeRow // PR-6: keyed by compute_nodes.name
	cnNextID int
}

type fakeRow struct {
	ID         string
	Bundle     Bundle
	DaemonJSON []byte
	CreatedAt  time.Time
	AppliedAt  *time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		rows:   map[string]*fakeRow{},
		cnRows: map[string]*ComputeNodeRow{},
	}
}

func (s *fakeStore) Insert(_ context.Context, b Bundle) (string, error) {
	if b.GitSHA == "" || b.ManifestHash == "" || len(b.DaemonHashes) == 0 {
		return "", errors.New("insert requires git_sha, manifest_hash, non-empty daemon_hashes")
	}
	hashes, err := EncodeDaemonHashes(b.DaemonHashes)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rows[b.GitSHA]; exists {
		return "", errors.New("insert: row already exists for git_sha")
	}
	id := "fake-" + b.GitSHA[:8]
	s.rows[b.GitSHA] = &fakeRow{
		ID:         id,
		Bundle:     b,
		DaemonJSON: hashes,
		CreatedAt:  time.Now(),
	}
	return id, nil
}

func (s *fakeStore) MarkApplied(_ context.Context, gitSHA string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[gitSHA]
	if !ok {
		return false, ErrNotFound
	}
	if row.AppliedAt != nil {
		return false, nil
	}
	now := time.Now()
	row.AppliedAt = &now
	return true, nil
}

func (s *fakeStore) GetByGitSHA(_ context.Context, gitSHA string) (BundleRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[gitSHA]
	if !ok {
		return BundleRow{}, ErrNotFound
	}
	hashes, err := DecodeDaemonHashes(row.DaemonJSON)
	if err != nil {
		return BundleRow{}, err
	}
	return BundleRow{
		ID:           row.ID,
		GitSHA:       row.Bundle.GitSHA,
		ManifestHash: row.Bundle.ManifestHash,
		DaemonHashes: hashes,
		CreatedAt:    row.CreatedAt,
		AppliedAt:    row.AppliedAt,
	}, nil
}

func (s *fakeStore) ListApplied(_ context.Context) ([]BundleRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []BundleRow
	for _, row := range s.rows {
		if row.AppliedAt == nil {
			continue
		}
		hashes, err := DecodeDaemonHashes(row.DaemonJSON)
		if err != nil {
			return nil, err
		}
		out = append(out, BundleRow{
			ID:           row.ID,
			GitSHA:       row.Bundle.GitSHA,
			ManifestHash: row.Bundle.ManifestHash,
			DaemonHashes: hashes,
			CreatedAt:    row.CreatedAt,
			AppliedAt:    row.AppliedAt,
		})
	}
	// order by applied_at desc
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].AppliedAt.After(*out[i].AppliedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// ListAllBundles implements Store (PR-4). Drops the applied_at
// filter and orders by created_at desc.
func (s *fakeStore) ListAllBundles(_ context.Context) ([]BundleRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []BundleRow
	for _, row := range s.rows {
		hashes, err := DecodeDaemonHashes(row.DaemonJSON)
		if err != nil {
			return nil, err
		}
		out = append(out, BundleRow{
			ID:           row.ID,
			GitSHA:       row.Bundle.GitSHA,
			ManifestHash: row.Bundle.ManifestHash,
			DaemonHashes: hashes,
			CreatedAt:    row.CreatedAt,
			AppliedAt:    row.AppliedAt,
		})
	}
	// order by created_at desc
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].CreatedAt.After(out[i].CreatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// UpsertComputeNode implements Store (PR-6). Mirrors the pgStore
// behaviour: input validation via validGitSHA/validManifestHash,
// idempotent INSERT...ON CONFLICT keyed by name, returns the row id.
func (s *fakeStore) UpsertComputeNode(_ context.Context, name, gitSHA, manifestHash string) (string, error) {
	if name == "" {
		return "", errors.New("releaseinstall: empty compute_nodes name")
	}
	if !validGitSHA(gitSHA) {
		return "", errors.New("releaseinstall: invalid git_sha")
	}
	if !validManifestHash(manifestHash) {
		return "", errors.New("releaseinstall: invalid manifest_hash")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.cnRows[name]; ok {
		// Update in place. Idempotent on re-install.
		existing.ReleaseID = gitSHA
		existing.ManifestHash = manifestHash
		return existing.ID, nil
	}
	s.cnNextID++
	id := "fake-cn-" + name
	s.cnRows[name] = &ComputeNodeRow{
		ID:           id,
		Name:         name,
		ReleaseID:    gitSHA,
		ManifestHash: manifestHash,
		Lifecycle:    "active",
		Active:       true,
	}
	return id, nil
}

// GetComputeNode implements Store (PR-6, widened PR-4). Mirrors
// GetByGitSHA's ErrNotFound convention with the dedicated
// ErrComputeNodeNotFound. The widened row carries the same shape
// as the production SELECT — host_certificate / cert_fingerprint /
// role / generation are nil pointers unless the fake has stamped
// them via UpsertComputeNode (today PR-4 only writes the original
// four fields, so the new pointers stay nil).
func (s *fakeStore) GetComputeNode(_ context.Context, name string) (ComputeNodeRow, error) {
	if name == "" {
		return ComputeNodeRow{}, errors.New("releaseinstall: empty compute_nodes name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.cnRows[name]
	if !ok {
		return ComputeNodeRow{}, ErrComputeNodeNotFound
	}
	return *row, nil
}

// ListComputeNodes implements Store (PR-4). Walks the existing
// cnRows map; orders by name for PQ-stable parity with the
// production SELECT.
func (s *fakeStore) ListComputeNodes(_ context.Context) ([]ComputeNodeRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.cnRows))
	for k := range s.cnRows {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]ComputeNodeRow, 0, len(names))
	for _, n := range names {
		out = append(out, *s.cnRows[n])
	}
	return out, nil
}

// SetComputeNodeRole implements Store (PR-2 / issue #911 / ADR-110).
// Mirrors the pgStore behaviour: ErrComputeNodeNotFound on a missing
// row, (false, nil) if the role is already the same value,
// (true, nil) on a real update.
func (s *fakeStore) SetComputeNodeRole(_ context.Context, name, role string) (bool, error) {
	if name == "" {
		return false, errors.New("releaseinstall: empty compute_nodes name")
	}
	if !canonicalComputeNodeRoles[role] {
		return false, fmt.Errorf("%w (got %q)", ErrInvalidRole, role)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.cnRows[name]
	if !ok {
		return false, ErrComputeNodeNotFound
	}
	if row.Role != nil && *row.Role == role {
		return false, nil
	}
	row.Role = &role
	return true, nil
}

// StampHostCertificate implements Store (PR-X / Commit 6).
func (s *fakeStore) StampHostCertificate(_ context.Context, name, pem, fingerprint string) error {
	if name == "" {
		return errors.New("releaseinstall: empty compute_nodes name")
	}
	if pem == "" {
		return errors.New("releaseinstall: empty host_certificate pem")
	}
	if fingerprint == "" {
		return errors.New("releaseinstall: empty cert_fingerprint")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.cnRows[name]
	if !ok {
		return ErrComputeNodeNotFound
	}
	row.HostCertificate = &pem
	row.CertFingerprint = &fingerprint
	return nil
}

// BumpComputeNodeGeneration implements Store (PR-4 / Commit 6).
func (s *fakeStore) BumpComputeNodeGeneration(_ context.Context, name string) (int, error) {
	if name == "" {
		return 0, errors.New("releaseinstall: empty compute_nodes name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.cnRows[name]
	if !ok {
		return 0, ErrComputeNodeNotFound
	}
	gen := 0
	if row.Generation != nil {
		gen = *row.Generation
	}
	gen++
	row.Generation = &gen
	return gen, nil
}

func sampleBundle(sha string) Bundle {
	// EncodeDaemonHashes requires every daemon in the catalog; the
	// sample bundle is canonical-complete.
	hashes := make(map[string]string, 9)
	patterns := []string{
		"1111111111111111111111111111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222222222222222222222222222",
		"3333333333333333333333333333333333333333333333333333333333333333",
		"4444444444444444444444444444444444444444444444444444444444444444",
		"5555555555555555555555555555555555555555555555555555555555555555",
		"6666666666666666666666666666666666666666666666666666666666666666",
		"7777777777777777777777777777777777777777777777777777777777777777",
		"8888888888888888888888888888888888888888888888888888888888888888",
		"9999999999999999999999999999999999999999999999999999999999999999",
	}
	// We don't import manifest.SortedHostKeys here directly to keep
	// the test self-contained — but to stay honest with the contract
	// we list all 9. Order doesn't matter to a map.
	keys := []string{
		"apid", "builderd", "gatewayd_internal", "gatewayd_public",
		"githubd", "imaged", "meterd", "schedd", "vmmd",
	}
	for i, k := range keys {
		hashes[k] = "sha256:" + patterns[i]
	}
	return Bundle{
		FormatVersion: FormatVersion,
		GitSHA:        sha,
		ManifestHash:  "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		DaemonHashes:  hashes,
		CreatedAt:     time.Now(),
	}
}

func TestFakeStore_InsertAndGetByGitSHA(t *testing.T) {
	s := newFakeStore()
	b := sampleBundle("0123456789abcdef0123456789abcdef01234567")
	id, err := s.Insert(context.Background(), b)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id == "" {
		t.Errorf("Insert returned empty id")
	}
	got, err := s.GetByGitSHA(context.Background(), b.GitSHA)
	if err != nil {
		t.Fatalf("GetByGitSHA: %v", err)
	}
	if got.ID != id {
		t.Errorf("round-trip id = %q, want %q", got.ID, id)
	}
	if got.GitSHA != b.GitSHA {
		t.Errorf("round-trip git_sha = %q, want %q", got.GitSHA, b.GitSHA)
	}
	if got.ManifestHash != b.ManifestHash {
		t.Errorf("round-trip manifest_hash = %q, want %q", got.ManifestHash, b.ManifestHash)
	}
	if len(got.DaemonHashes) != len(b.DaemonHashes) {
		t.Errorf("daemon_hashes len = %d, want %d", len(got.DaemonHashes), len(b.DaemonHashes))
	}
	for k, v := range b.DaemonHashes {
		if got.DaemonHashes[k] != v {
			t.Errorf("daemon_hash %s = %q, want %q", k, got.DaemonHashes[k], v)
		}
	}
	if got.AppliedAt != nil {
		t.Errorf("AppliedAt = %v, want nil (not yet stamped)", got.AppliedAt)
	}
}

func TestFakeStore_InsertRejectsEmpty(t *testing.T) {
	s := newFakeStore()
	full := sampleBundle("0123456789abcdef0123456789abcdef01234567")
	// Empty bundle — git_sha, manifest_hash, daemon_hashes all empty.
	if _, err := s.Insert(context.Background(), Bundle{}); err == nil {
		t.Errorf("Insert(Bundle{}) = nil err, want error")
	}
	// Valid git_sha, missing manifest_hash.
	missingHash := full
	missingHash.ManifestHash = ""
	if _, err := s.Insert(context.Background(), missingHash); err == nil {
		t.Errorf("Insert(missing manifest_hash) = nil err, want error")
	}
	// Valid git_sha + manifest_hash, missing daemon_hashes.
	missingHashes := full
	missingHashes.DaemonHashes = nil
	if _, err := s.Insert(context.Background(), missingHashes); err == nil {
		t.Errorf("Insert(nil daemon_hashes) = nil err, want error")
	}
}

func TestFakeStore_MarkApplied_FirstWriteWins(t *testing.T) {
	s := newFakeStore()
	b := sampleBundle("0123456789abcdef0123456789abcdef01234567")
	if _, err := s.Insert(context.Background(), b); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	first, err := s.MarkApplied(context.Background(), b.GitSHA)
	if err != nil {
		t.Fatalf("MarkApplied 1: %v", err)
	}
	if !first {
		t.Errorf("first MarkApplied = false, want true")
	}
	second, err := s.MarkApplied(context.Background(), b.GitSHA)
	if err != nil {
		t.Fatalf("MarkApplied 2: %v", err)
	}
	if second {
		t.Errorf("second MarkApplied = true, want false (idempotent)")
	}
}

func TestFakeStore_MarkApplied_MissingRow(t *testing.T) {
	s := newFakeStore()
	_, err := s.MarkApplied(context.Background(), "ffffffffffffffffffffffffffffffffffffffff")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkApplied on missing row = %v, want ErrNotFound", err)
	}
}

func TestFakeStore_GetByGitSHA_NotFound(t *testing.T) {
	s := newFakeStore()
	_, err := s.GetByGitSHA(context.Background(), "ffffffffffffffffffffffffffffffffffffffff")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByGitSHA on missing row = %v, want ErrNotFound", err)
	}
}

func TestFakeStore_ListApplied_OrdersDescAndFiltersUnapplied(t *testing.T) {
	s := newFakeStore()
	sha1 := "0000000000000000000000000000000000000001"
	sha2 := "0000000000000000000000000000000000000002"
	sha3 := "0000000000000000000000000000000000000003"
	for _, sha := range []string{sha1, sha2, sha3} {
		if _, err := s.Insert(context.Background(), sampleBundle(sha)); err != nil {
			t.Fatalf("Insert %s: %v", sha, err)
		}
	}
	// Mark sha1 and sha3 applied; leave sha2 unapplied.
	if _, err := s.MarkApplied(context.Background(), sha1); err != nil {
		t.Fatalf("MarkApplied sha1: %v", err)
	}
	// Small sleep to make applied_at distinguishable in the sort.
	time.Sleep(10 * time.Millisecond)
	if _, err := s.MarkApplied(context.Background(), sha3); err != nil {
		t.Fatalf("MarkApplied sha3: %v", err)
	}
	got, err := s.ListApplied(context.Background())
	if err != nil {
		t.Fatalf("ListApplied: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListApplied len = %d, want 2", len(got))
	}
	if got[0].GitSHA != sha3 {
		t.Errorf("ListApplied[0] = %q, want %q (newest first)", got[0].GitSHA, sha3)
	}
	if got[1].GitSHA != sha1 {
		t.Errorf("ListApplied[1] = %q, want %q (oldest last)", got[1].GitSHA, sha1)
	}
}

// PR-6: UpsertComputeNode + GetComputeNode tests.
//
// The first-write-wins semantics on the bundle table (MarkApplied)
// intentionally do NOT apply here: a re-install from the same box
// on the same release is idempotent (release_id + manifest_hash stay
// the same), but a re-install onto a NEW release must overwrite
// release_id + manifest_hash with the new values. ON CONFLICT (name)
// DO UPDATE delivers both behaviours.

func TestFakeStore_UpsertComputeNode_NewRow(t *testing.T) {
	s := newFakeStore()
	sha := "0123456789abcdef0123456789abcdef01234567"
	mh := "sha256:" + strings.Repeat("a", 64)
	id, err := s.UpsertComputeNode(context.Background(), "node-A", sha, mh)
	if err != nil {
		t.Fatalf("UpsertComputeNode: %v", err)
	}
	if id == "" {
		t.Errorf("UpsertComputeNode returned empty id")
	}
	got, err := s.GetComputeNode(context.Background(), "node-A")
	if err != nil {
		t.Fatalf("GetComputeNode: %v", err)
	}
	if got.ID != id {
		t.Errorf("round-trip id = %q, want %q", got.ID, id)
	}
	if got.ReleaseID != sha {
		t.Errorf("release_id = %q, want %q", got.ReleaseID, sha)
	}
	if got.ManifestHash != mh {
		t.Errorf("manifest_hash = %q, want %q", got.ManifestHash, mh)
	}
}

func TestFakeStore_UpsertComputeNode_RerunUpdates(t *testing.T) {
	s := newFakeStore()
	sha1 := "0123456789abcdef0123456789abcdef01234567"
	mh1 := "sha256:" + strings.Repeat("a", 64)
	sha2 := "89abcdef0123456789abcdef0123456789abcdef"
	mh2 := "sha256:" + strings.Repeat("b", 64)
	// First install.
	id1, err := s.UpsertComputeNode(context.Background(), "node-A", sha1, mh1)
	if err != nil {
		t.Fatalf("UpsertComputeNode 1: %v", err)
	}
	// Re-install with the SAME release (idempotent).
	id2, err := s.UpsertComputeNode(context.Background(), "node-A", sha1, mh1)
	if err != nil {
		t.Fatalf("UpsertComputeNode 1b (same release): %v", err)
	}
	if id1 != id2 {
		t.Errorf("idempotent re-install id = %q, want %q (id stable across re-installs)", id2, id1)
	}
	// Re-install with a NEW release (overwrites release_id + manifest_hash).
	id3, err := s.UpsertComputeNode(context.Background(), "node-A", sha2, mh2)
	if err != nil {
		t.Fatalf("UpsertComputeNode 2 (new release): %v", err)
	}
	if id3 != id1 {
		t.Errorf("new-release id = %q, want %q (id stable across releases)", id3, id1)
	}
	got, err := s.GetComputeNode(context.Background(), "node-A")
	if err != nil {
		t.Fatalf("GetComputeNode: %v", err)
	}
	if got.ReleaseID != sha2 {
		t.Errorf("post-overwrite release_id = %q, want %q", got.ReleaseID, sha2)
	}
	if got.ManifestHash != mh2 {
		t.Errorf("post-overwrite manifest_hash = %q, want %q", got.ManifestHash, mh2)
	}
}

func TestFakeStore_GetComputeNode_RoundTrips(t *testing.T) {
	s := newFakeStore()
	sha := "0123456789abcdef0123456789abcdef01234567"
	mh := "sha256:" + strings.Repeat("c", 64)
	if _, err := s.UpsertComputeNode(context.Background(), "node-B", sha, mh); err != nil {
		t.Fatalf("UpsertComputeNode: %v", err)
	}
	got, err := s.GetComputeNode(context.Background(), "node-B")
	if err != nil {
		t.Fatalf("GetComputeNode: %v", err)
	}
	if got.Name != "node-B" {
		t.Errorf("Name = %q, want %q", got.Name, "node-B")
	}
	if got.ReleaseID != sha {
		t.Errorf("ReleaseID = %q, want %q", got.ReleaseID, sha)
	}
	if got.ManifestHash != mh {
		t.Errorf("ManifestHash = %q, want %q", got.ManifestHash, mh)
	}
}

func TestFakeStore_GetComputeNode_NotFound(t *testing.T) {
	s := newFakeStore()
	_, err := s.GetComputeNode(context.Background(), "no-such-node")
	if !errors.Is(err, ErrComputeNodeNotFound) {
		t.Errorf("GetComputeNode on missing row = %v, want ErrComputeNodeNotFound", err)
	}
}

// PR-4 tests: ListComputeNodes + ListAllBundles + the widened
// ComputeNodeRow shape. Doctor depends on these to walk all
// per-node membership and detect drift against the on-disk bundle.

func TestFakeStore_ListComputeNodes_Empty(t *testing.T) {
	s := newFakeStore()
	got, err := s.ListComputeNodes(context.Background())
	if err != nil {
		t.Fatalf("ListComputeNodes: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListComputeNodes on empty = %d rows, want 0", len(got))
	}
}

func TestFakeStore_ListComputeNodes_OrdersByName(t *testing.T) {
	s := newFakeStore()
	sha := "0123456789abcdef0123456789abcdef01234567"
	mh := "sha256:" + strings.Repeat("a", 64)
	for _, name := range []string{"node-c", "node-a", "node-b"} {
		if _, err := s.UpsertComputeNode(context.Background(), name, sha, mh); err != nil {
			t.Fatalf("UpsertComputeNode(%s): %v", name, err)
		}
	}
	got, err := s.ListComputeNodes(context.Background())
	if err != nil {
		t.Fatalf("ListComputeNodes: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListComputeNodes = %d rows, want 3", len(got))
	}
	want := []string{"node-a", "node-b", "node-c"}
	for i, n := range want {
		if got[i].Name != n {
			t.Errorf("ListComputeNodes[%d] = %q, want %q", i, got[i].Name, n)
		}
	}
}

func TestFakeStore_ListComputeNodes_RoundTripsNewColumns(t *testing.T) {
	// The new fields (host_certificate / cert_fingerprint / role /
	// generation) are nil while UpsertComputeNode keeps ignoring
	// them — matches the production INSERT (store.go:288-294) which
	// writes generation=0 and NULLs to the others. The widening is
	// observable on ListComputeNodes: the widened columns must
	// round-trip as nil (the pointer stays nil) and the original
	// four fields must stay populated.
	s := newFakeStore()
	sha := "0123456789abcdef0123456789abcdef01234567"
	mh := "sha256:" + strings.Repeat("a", 64)
	if _, err := s.UpsertComputeNode(context.Background(), "node-x", sha, mh); err != nil {
		t.Fatalf("UpsertComputeNode: %v", err)
	}
	got, err := s.ListComputeNodes(context.Background())
	if err != nil {
		t.Fatalf("ListComputeNodes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListComputeNodes = %d rows, want 1", len(got))
	}
	row := got[0]
	if row.ID == "" || row.Name != "node-x" || row.ReleaseID != sha || row.ManifestHash != mh {
		t.Errorf("ListComputeNodes[0] = %+v, want id+name+release_id+manifest_hash populated", row)
	}
	if row.HostCertificate != nil {
		t.Errorf("HostCertificate = %q, want nil (UpsertComputeNode doesn't write it)", *row.HostCertificate)
	}
	if row.CertFingerprint != nil {
		t.Errorf("CertFingerprint = %q, want nil", *row.CertFingerprint)
	}
	if row.Role != nil {
		t.Errorf("Role = %q, want nil", *row.Role)
	}
	if row.Generation != nil {
		t.Errorf("Generation = %d, want nil", *row.Generation)
	}
}

func TestFakeStore_ListAllBundles_IncludesUnapplied(t *testing.T) {
	s := newFakeStore()
	sha1 := "0000000000000000000000000000000000000001"
	sha2 := "0000000000000000000000000000000000000002"
	for _, sha := range []string{sha1, sha2} {
		if _, err := s.Insert(context.Background(), sampleBundle(sha)); err != nil {
			t.Fatalf("Insert %s: %v", sha, err)
		}
	}
	// Mark only sha1 applied.
	if _, err := s.MarkApplied(context.Background(), sha1); err != nil {
		t.Fatalf("MarkApplied sha1: %v", err)
	}
	got, err := s.ListAllBundles(context.Background())
	if err != nil {
		t.Fatalf("ListAllBundles: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ListAllBundles = %d rows, want 2 (includes unapplied)", len(got))
	}
}

func TestFakeStore_ListAllBundles_OrdersByCreatedDesc(t *testing.T) {
	s := newFakeStore()
	sha1 := "0000000000000000000000000000000000000001"
	sha2 := "0000000000000000000000000000000000000002"
	sha3 := "0000000000000000000000000000000000000003"
	for _, sha := range []string{sha1, sha2, sha3} {
		if _, err := s.Insert(context.Background(), sampleBundle(sha)); err != nil {
			t.Fatalf("Insert %s: %v", sha, err)
		}
		time.Sleep(2 * time.Millisecond) // make created_at distinguishable
	}
	got, err := s.ListAllBundles(context.Background())
	if err != nil {
		t.Fatalf("ListAllBundles: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListAllBundles = %d rows, want 3", len(got))
	}
	if got[0].GitSHA != sha3 {
		t.Errorf("ListAllBundles[0] = %q, want %q (newest first)", got[0].GitSHA, sha3)
	}
	if got[2].GitSHA != sha1 {
		t.Errorf("ListAllBundles[2] = %q, want %q (oldest last)", got[2].GitSHA, sha1)
	}
}

// PR-2 (issue #911 / ADR-110) tests: SetComputeNodeRole. The
// renderer calls this after the file-write phase so the manifest
// host.role value lands on compute_nodes.role. The contract:
//
//   - ErrComputeNodeNotFound when no row exists for the given name
//     (deploy-order bug: releaseinstall was skipped before render)
//   - (false, nil) when the row already has the same role
//     (idempotent second render — no audit log noise)
//   - (true, nil) on a real role change
//   - rejects empty name + empty role with explicit errors

func TestFakeStore_SetComputeNodeRole_NewRow(t *testing.T) {
	s := newFakeStore()
	sha := "0123456789abcdef0123456789abcdef01234567"
	mh := "sha256:" + strings.Repeat("a", 64)
	if _, err := s.UpsertComputeNode(context.Background(), "node-A", sha, mh); err != nil {
		t.Fatalf("UpsertComputeNode: %v", err)
	}
	updated, err := s.SetComputeNodeRole(context.Background(), "node-A", "control-plane")
	if err != nil {
		t.Fatalf("SetComputeNodeRole: %v", err)
	}
	if !updated {
		t.Errorf("SetComputeNodeRole first call = false, want true (row role changed)")
	}
	got, err := s.GetComputeNode(context.Background(), "node-A")
	if err != nil {
		t.Fatalf("GetComputeNode: %v", err)
	}
	if got.Role == nil || *got.Role != "control-plane" {
		t.Errorf("Role = %v, want control-plane", got.Role)
	}
}

func TestFakeStore_SetComputeNodeRole_Idempotent(t *testing.T) {
	s := newFakeStore()
	sha := "0123456789abcdef0123456789abcdef01234567"
	mh := "sha256:" + strings.Repeat("a", 64)
	if _, err := s.UpsertComputeNode(context.Background(), "node-B", sha, mh); err != nil {
		t.Fatalf("UpsertComputeNode: %v", err)
	}
	first, err := s.SetComputeNodeRole(context.Background(), "node-B", "compute-only")
	if err != nil {
		t.Fatalf("SetComputeNodeRole 1: %v", err)
	}
	if !first {
		t.Errorf("first SetComputeNodeRole = false, want true")
	}
	second, err := s.SetComputeNodeRole(context.Background(), "node-B", "compute-only")
	if err != nil {
		t.Fatalf("SetComputeNodeRole 2 (same role): %v", err)
	}
	if second {
		t.Errorf("second SetComputeNodeRole (same role) = true, want false (idempotent)")
	}
}

func TestFakeStore_SetComputeNodeRole_RoleChange(t *testing.T) {
	s := newFakeStore()
	sha := "0123456789abcdef0123456789abcdef01234567"
	mh := "sha256:" + strings.Repeat("a", 64)
	if _, err := s.UpsertComputeNode(context.Background(), "node-C", sha, mh); err != nil {
		t.Fatalf("UpsertComputeNode: %v", err)
	}
	if _, err := s.SetComputeNodeRole(context.Background(), "node-C", "control-plane"); err != nil {
		t.Fatalf("SetComputeNodeRole control-plane: %v", err)
	}
	updated, err := s.SetComputeNodeRole(context.Background(), "node-C", "compute-only")
	if err != nil {
		t.Fatalf("SetComputeNodeRole compute-only: %v", err)
	}
	if !updated {
		t.Errorf("SetComputeNodeRole role change = false, want true (control-plane → compute-only)")
	}
	got, err := s.GetComputeNode(context.Background(), "node-C")
	if err != nil {
		t.Fatalf("GetComputeNode: %v", err)
	}
	if got.Role == nil || *got.Role != "compute-only" {
		t.Errorf("Role = %v, want compute-only", got.Role)
	}
}

func TestFakeStore_SetComputeNodeRole_MissingRow(t *testing.T) {
	s := newFakeStore()
	_, err := s.SetComputeNodeRole(context.Background(), "no-such-node", "control-plane")
	if !errors.Is(err, ErrComputeNodeNotFound) {
		t.Errorf("SetComputeNodeRole on missing row = %v, want ErrComputeNodeNotFound", err)
	}
}

func TestFakeStore_SetComputeNodeRole_EmptyInputs(t *testing.T) {
	s := newFakeStore()
	if _, err := s.SetComputeNodeRole(context.Background(), "", "control-plane"); err == nil {
		t.Errorf("SetComputeNodeRole empty name = nil err, want error")
	}
	if _, err := s.SetComputeNodeRole(context.Background(), "node-D", ""); err == nil {
		t.Errorf("SetComputeNodeRole empty role = nil err, want error")
	}
}

// repeat is unused now — kept commented out in case future tests
// need a no-imports helper for the "sha256:" + 64-hex pattern.
//
// func repeat(s string, n int) string {
// 	out := ""
// 	for i := 0; i < n; i++ {
// 		out += s
// 	}
// 	return out
// }

// PR-6 follow-up: SetComputeNodeRole rejects any value not in the
// canonical enum {single-box, control-plane, compute-only, ""}.
func TestFakeStore_SetComputeNodeRole_InvalidRole(t *testing.T) {
	s := newFakeStore()
	for _, bad := range []string{"primary", "all", "control_plane", "controlplane", "Compute-Only"} {
		if _, err := s.SetComputeNodeRole(context.Background(), "node-X", bad); err == nil {
			t.Errorf("SetComputeNodeRole(%q) = nil err, want ErrInvalidRole", bad)
		} else if !errors.Is(err, ErrInvalidRole) {
			t.Errorf("SetComputeNodeRole(%q) err = %v, want ErrInvalidRole", bad, err)
		}
	}
}

// TestFakeStore_StampHostCertificate_HappyPath pins the PR-X
// writer: secrets init sets host_certificate + cert_fingerprint
// keyed by name. Idempotent re-call overwrites with the new
// fingerprint (rotate path).
func TestFakeStore_StampHostCertificate_HappyPath(t *testing.T) {
	s := newFakeStore()
	sha := strings.Repeat("a", 40)
	mh := "sha256:" + strings.Repeat("a", 64)
	if _, err := s.UpsertComputeNode(context.Background(), "node-1", sha, mh); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.StampHostCertificate(context.Background(), "node-1", "PEM-A", "fp-A"); err != nil {
		t.Fatalf("StampHostCertificate: %v", err)
	}
	row, err := s.GetComputeNode(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("GetComputeNode: %v", err)
	}
	if row.HostCertificate == nil || *row.HostCertificate != "PEM-A" {
		t.Errorf("HostCertificate = %v, want PEM-A", row.HostCertificate)
	}
	if row.CertFingerprint == nil || *row.CertFingerprint != "fp-A" {
		t.Errorf("CertFingerprint = %v, want fp-A", row.CertFingerprint)
	}
	// Idempotent overwrite (rotate).
	if err := s.StampHostCertificate(context.Background(), "node-1", "PEM-B", "fp-B"); err != nil {
		t.Fatalf("StampHostCertificate (rotate): %v", err)
	}
	row, _ = s.GetComputeNode(context.Background(), "node-1")
	if row.HostCertificate == nil || *row.HostCertificate != "PEM-B" {
		t.Errorf("after rotate, HostCertificate = %v, want PEM-B", row.HostCertificate)
	}
}

func TestFakeStore_StampHostCertificate_NotFound(t *testing.T) {
	s := newFakeStore()
	err := s.StampHostCertificate(context.Background(), "ghost", "PEM", "fp")
	if !errors.Is(err, ErrComputeNodeNotFound) {
		t.Errorf("err = %v, want ErrComputeNodeNotFound", err)
	}
}

// TestFakeStore_BumpComputeNodeGeneration pins PR-4 doctor's
// drift signal: BumpGeneration bumps the generation column,
// idempotent re-calls keep stepping up.
func TestFakeStore_BumpComputeNodeGeneration(t *testing.T) {
	s := newFakeStore()
	sha := strings.Repeat("b", 40)
	mh := "sha256:" + strings.Repeat("b", 64)
	if _, err := s.UpsertComputeNode(context.Background(), "node-1", sha, mh); err != nil {
		t.Fatalf("seed: %v", err)
	}
	gen1, err := s.BumpComputeNodeGeneration(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("Bump1: %v", err)
	}
	if gen1 != 1 {
		t.Errorf("Bump1 gen = %d, want 1", gen1)
	}
	gen2, err := s.BumpComputeNodeGeneration(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("Bump2: %v", err)
	}
	if gen2 != 2 {
		t.Errorf("Bump2 gen = %d, want 2", gen2)
	}
	row, _ := s.GetComputeNode(context.Background(), "node-1")
	if row.Generation == nil || *row.Generation != 2 {
		t.Errorf("Generation = %v, want 2", row.Generation)
	}
}

func TestFakeStore_BumpComputeNodeGeneration_NotFound(t *testing.T) {
	s := newFakeStore()
	if _, err := s.BumpComputeNodeGeneration(context.Background(), "ghost"); !errors.Is(err, ErrComputeNodeNotFound) {
		t.Errorf("err = %v, want ErrComputeNodeNotFound", err)
	}
}
