package runtimequalification

// adr: 687

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type fakeNativeSession struct {
	fixture                                            *evidenceFixture
	calls                                              []string
	prepareErr, testErr, leakErr, recheckErr, closeErr error
	testExit, leakExit                                 int
	skip                                               bool
	afterTest                                          func()
}

func (s *fakeNativeSession) Identity() NativeIdentity {
	return NativeIdentity{s.fixture.report.Native.HostID, s.fixture.report.Native.KernelBootID}
}
func (s *fakeNativeSession) Prepare(context.Context, Artifacts, Fixture) error {
	s.calls = append(s.calls, "prepare")
	return s.prepareErr
}
func (s *fakeNativeSession) Test(context.Context) (CommandResult, error) {
	s.calls = append(s.calls, "test")
	events := append([]testEvent{}, s.fixture.events...)
	now := time.Now().UTC()
	for i := range events {
		events[i].Time = now
	}
	if s.skip {
		events[3].Action = "skip"
	}
	// The fixture trace/identity is synthetic. Only the concrete native owner
	// measures physical facts; this seam tests orchestration and signature gates.
	raw := marshalEventsForFake(events)
	if s.afterTest != nil {
		s.afterTest()
	}
	return CommandResult{ExitCode: s.testExit, Stdout: raw, Stderr: []byte("synthetic test stderr")}, s.testErr
}
func marshalEventsForFake(events []testEvent) []byte {
	var out bytes.Buffer
	for _, event := range events {
		raw, _ := json.Marshal(event)
		out.Write(raw)
		out.WriteByte('\n')
	}
	return out.Bytes()
}
func (s *fakeNativeSession) Leakcheck(ctx context.Context) (CommandResult, error) {
	s.calls = append(s.calls, "leakcheck")
	if ctx.Err() != nil {
		return CommandResult{ExitCode: -1}, ctx.Err()
	}
	raw := s.fixture.leak
	if s.leakExit != 0 {
		raw = []byte("LEAK: synthetic resource\nleakcheck FAILED\n")
	}
	return CommandResult{ExitCode: s.leakExit, Stdout: raw}, s.leakErr
}
func (s *fakeNativeSession) Recheck(context.Context) error {
	s.calls = append(s.calls, "recheck")
	return s.recheckErr
}
func (s *fakeNativeSession) Close(ctx context.Context) error {
	s.calls = append(s.calls, "close")
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return s.closeErr
}

func collectionFixture(t *testing.T, f *evidenceFixture) (NativeConfig, Fixture) {
	t.Helper()
	n := f.report.Native
	dir := t.TempDir()
	c := NativeConfig{SourceDirectory: filepath.Join(dir, "source"), GoBinary: filepath.Join(dir, "go"), GoSHA256: strings.Repeat("a", 64), KernelPath: filepath.Join(dir, "kernel"), FirecrackerVersion: "1.7.0", OutputDirectory: filepath.Join(dir, "evidence")}
	fixture := Fixture{Target: f.release, RunID: n.RunID, HostID: n.HostID, SourceCommit: n.SourceCommit, DeploymentID: n.DeploymentID, LayerKey: n.LayerKey, LayerSHA256: n.LayerSHA256, KernelSHA256: n.KernelSHA256, FirecrackerSHA256: n.FirecrackerSHA256}
	return c, fixture
}

func TestCollectSignsOnlyAfterCleanRetirementAndRestoration(t *testing.T) {
	f := newEvidenceFixture(t)
	c, fixture := collectionFixture(t, f)
	session := &fakeNativeSession{fixture: f}
	bundle, err := Collect(t.Context(), f.store, f.artifacts, c, fixture, f.trust, f.key, func(context.Context, NativeConfig, Fixture) (NativeSession, error) { return session, nil })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(session.calls, []string{"prepare", "test", "leakcheck", "recheck", "close"}) {
		t.Fatal("wrong acceptance ordering", session.calls)
	}
	report, _, err := VerifyEnvelope(bundle.Envelope, f.trust, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyLogs(report, bundle.TestMetal, bundle.Leakcheck); err != nil {
		t.Fatal(err)
	}
	persisted, err := os.ReadFile(filepath.Join(c.OutputDirectory, "report.json"))
	if err != nil || !bytes.Equal(persisted, bundle.Envelope) {
		t.Fatal("signing preceded retention", err)
	}
	if _, err := f.store.RuntimeReleaseQualification(t.Context(), f.release.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("collector wrote receipt before import", err)
	}
	receipt, err := Import(t.Context(), f.store, f.artifacts, Inputs{bytes.NewReader(bundle.Envelope), bytes.NewReader(bundle.TestMetal), bytes.NewReader(bundle.Leakcheck)}, f.trust)
	if err != nil || receipt.ReportSHA256 != SHA256(bundle.Envelope) {
		t.Fatal("completed bundle could not import", err)
	}
}

func TestCollectFailureCannotSign(t *testing.T) {
	failure := errors.New("synthetic native failure")
	for _, tc := range []struct {
		name string
		edit func(*fakeNativeSession)
	}{
		{"prepare", func(s *fakeNativeSession) { s.prepareErr = failure }},
		{"test process", func(s *fakeNativeSession) { s.testErr = failure }},
		{"test exit", func(s *fakeNativeSession) { s.testExit = 1 }},
		{"skipped test", func(s *fakeNativeSession) { s.skip = true }},
		{"leak process", func(s *fakeNativeSession) { s.leakErr = failure }},
		{"leak exit", func(s *fakeNativeSession) { s.leakExit = 1 }},
		{"host changed", func(s *fakeNativeSession) { s.recheckErr = failure }},
		{"restoration", func(s *fakeNativeSession) { s.closeErr = failure }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEvidenceFixture(t)
			c, fixture := collectionFixture(t, f)
			session := &fakeNativeSession{fixture: f}
			tc.edit(session)
			bundle, err := Collect(t.Context(), f.store, f.artifacts, c, fixture, f.trust, f.key, func(context.Context, NativeConfig, Fixture) (NativeSession, error) { return session, nil })
			if err == nil || len(bundle.Envelope) != 0 {
				t.Fatal("failure signed success", err)
			}
			if _, err := os.Stat(filepath.Join(c.OutputDirectory, "report.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failure retained signed success", err)
			}
			if session.calls[len(session.calls)-1] != "close" {
				t.Fatal("failed owner not closed", session.calls)
			}
			if tc.name != "prepare" && !strings.Contains(strings.Join(session.calls, ","), "test,leakcheck") {
				t.Fatal("final leakcheck omitted after failed test", session.calls)
			}
		})
	}
}

func TestCollectCancellationUsesFreshCleanupContext(t *testing.T) {
	f := newEvidenceFixture(t)
	c, fixture := collectionFixture(t, f)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	session := &fakeNativeSession{fixture: f, afterTest: cancel}
	bundle, err := Collect(ctx, f.store, f.artifacts, c, fixture, f.trust, f.key, func(context.Context, NativeConfig, Fixture) (NativeSession, error) { return session, nil })
	if !errors.Is(err, context.Canceled) || len(bundle.Envelope) != 0 || session.calls[len(session.calls)-1] != "close" {
		t.Fatal("canceled run signed or did not clean up", err, session.calls)
	}
}

func TestCollectRejectsPinsReplayAndCatalogueBeforeNativeOpen(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*testing.T, *evidenceFixture, *NativeConfig, *Fixture)
	}{
		{"wrong source pin", func(_ *testing.T, f *evidenceFixture, _ *NativeConfig, _ *Fixture) {
			f.trust.SourceCommit = strings.Repeat("d", 40)
		}},
		{"wrong signing key", func(_ *testing.T, f *evidenceFixture, _ *NativeConfig, _ *Fixture) {
			f.trust.PublicKey = make([]byte, 32)
		}},
		{"bad Go pin", func(_ *testing.T, _ *evidenceFixture, c *NativeConfig, _ *Fixture) { c.GoSHA256 = "latest" }},
		{"bad Firecracker version", func(_ *testing.T, _ *evidenceFixture, c *NativeConfig, _ *Fixture) { c.FirecrackerVersion = "latest" }},
		{"relative source", func(_ *testing.T, _ *evidenceFixture, c *NativeConfig, _ *Fixture) { c.SourceDirectory = "source" }},
		{"output inside source", func(_ *testing.T, _ *evidenceFixture, c *NativeConfig, _ *Fixture) {
			c.OutputDirectory = filepath.Join(c.SourceDirectory, "evidence")
		}},
		{"replayed output", func(t *testing.T, _ *evidenceFixture, c *NativeConfig, _ *Fixture) {
			if err := os.Mkdir(c.OutputDirectory, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong layer binding", func(_ *testing.T, _ *evidenceFixture, _ *NativeConfig, f *Fixture) { f.LayerKey = "unpublished.ext4" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEvidenceFixture(t)
			c, fixture := collectionFixture(t, f)
			tc.edit(t, f, &c, &fixture)
			opened := false
			_, err := Collect(t.Context(), f.store, f.artifacts, c, fixture, f.trust, f.key, func(context.Context, NativeConfig, Fixture) (NativeSession, error) {
				opened = true
				return &fakeNativeSession{fixture: f}, nil
			})
			if err == nil || opened {
				t.Fatal("invalid request reached native owner", err)
			}
		})
	}
}
