package runtimequalification

// adr: 740

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// All keys, host observations and artifacts here are synthetic unit fixtures.
// Signing them tests the trust gate; it does not establish native acceptance.
type evidenceFixture struct {
	store            fixtureStore
	artifacts        *testArtifacts
	release          state.RuntimeRelease
	report           Report
	events           []testEvent
	metal, leak, raw []byte
	key              ed25519.PrivateKey
	trust            Trust
}

type fixtureStore interface {
	state.Store
	state.RuntimeReleaseStore
	state.RuntimeReleaseQualificationStore
}

func newEvidenceFixture(t *testing.T) *evidenceFixture {
	t.Helper()
	return newEvidenceFixtureWithStore(t, state.NewMemStore())
}

func newEvidenceFixtureWithStore(t *testing.T, seed fixtureStore) *evidenceFixture {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &evidenceFixture{store: seed, artifacts: &testArtifacts{objects: map[string][]byte{}}, key: key}
	ctx := t.Context()
	account, err := f.store.CreateAccount(ctx, uuid.NewString()+"@qualification.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := f.store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "native-" + uuid.NewString()[:8], Type: state.AppTypeFunction, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := f.store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployImaging})
	if err != nil {
		t.Fatal(err)
	}
	layerKey := "apps/" + app.Slug + "/" + dep.ID + ".ext4"
	base, layer := []byte("synthetic base bytes"), []byte("synthetic fixture layer bytes")
	r := state.RuntimeRelease{Runtime: "node22", Architecture: "amd64", SourceRef: "ghcr.io/test/node@sha256:" + strings.Repeat("a", 64), GuestInitSHA256: SHA256([]byte("synthetic init")), LayoutVersion: "test", BaseSHA256: SHA256(base)}
	r.ID = r.Identity()
	f.release, err = f.store.PublishRuntimeRelease(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetDeploymentRootfs(ctx, dep.ID, "/test/layer", layerKey, int64(len(layer))); err != nil {
		t.Fatal(err)
	}
	if err := f.store.BindDeploymentRuntimeRelease(ctx, dep.ID, layerKey, r.ID); err != nil {
		t.Fatal(err)
	}
	f.artifacts.objects[r.BaseKey()] = base
	f.artifacts.objects[layerKey] = layer
	now := time.Now().UTC().Truncate(time.Microsecond)
	zero := 0
	f.report = Report{Version: Version, Profile: state.RuntimeQualificationProfile, StartedAt: now.Add(-time.Minute), CompletedAt: now.Add(-time.Second), TestMetalExitCode: &zero, LeakcheckExitCode: &zero,
		Native: Observation{RunID: uuid.NewString(), ReleaseID: r.ID, HostID: uuid.NewString(), KernelBootID: uuid.NewString(), SourceCommit: strings.Repeat("b", 40), DeploymentID: dep.ID, LayerKey: layerKey, BaseSHA256: r.BaseSHA256, GuestInitSHA256: r.GuestInitSHA256, LayerSHA256: SHA256(layer), KernelSHA256: SHA256([]byte("kernel")), FirecrackerSHA256: SHA256([]byte("firecracker")), OS: "linux", Architecture: "amd64", Virtualization: "none", KVM: true, ColdBoot: true, Ready: true, Retired: true}}
	observation, err := json.Marshal(f.report.Native)
	if err != nil {
		t.Fatal(err)
	}
	f.events = []testEvent{{Action: "start"}, {Action: "run", Test: NativeTest}, {Action: "output", Test: NativeTest, Output: "    native_test.go:1: " + ObservationMarker + string(observation) + "\n"}, {Action: "pass", Test: NativeTest}, {Action: "pass"}}
	for i := range f.events {
		f.events[i].Package = NativePackage
		f.events[i].Time = f.report.StartedAt.Add(time.Duration(i+1) * time.Millisecond)
	}
	f.metal = marshalEvents(t, f.events)
	f.leak = []byte(LeakcheckSuccess + "\n")
	f.report.TestMetalSHA256 = SHA256(f.metal)
	f.report.LeakcheckSHA256 = SHA256(f.leak)
	f.trust = Trust{PublicKey: pub, ReleaseID: r.ID, HostID: f.report.Native.HostID, SourceCommit: f.report.Native.SourceCommit, RunID: f.report.Native.RunID}
	f.raw, err = EncodeEnvelope(f.report, f.metal, f.leak, key)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func marshalEvents(t *testing.T, events []testEvent) []byte {
	t.Helper()
	var out bytes.Buffer
	for _, event := range events {
		if err := json.NewEncoder(&out).Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	return out.Bytes()
}
func signUnchecked(t *testing.T, payload []byte, key ed25519.PrivateKey) []byte {
	t.Helper()
	pub := key.Public().(ed25519.PublicKey)
	raw, err := json.Marshal(Envelope{Version: Version, KeyID: SHA256(pub), Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, signatureInput(payload)))})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func resign(t *testing.T, f *evidenceFixture) {
	t.Helper()
	raw, err := json.Marshal(f.report)
	if err != nil {
		t.Fatal(err)
	}
	f.raw = signUnchecked(t, raw, f.key)
}
func (f *evidenceFixture) inputs() Inputs {
	return Inputs{bytes.NewReader(f.raw), bytes.NewReader(f.metal), bytes.NewReader(f.leak)}
}

type testArtifacts struct {
	objects            map[string][]byte
	gets, puts         []string
	getError, putError error
	corruptWrite       bool
	afterPut           func()
}

func (a *testArtifacts) Get(_ context.Context, key string) (io.ReadCloser, error) {
	a.gets = append(a.gets, key)
	if a.getError != nil {
		return nil, a.getError
	}
	b, ok := a.objects[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (a *testArtifacts) Put(_ context.Context, key string, r io.Reader) error {
	a.puts = append(a.puts, key)
	if a.putError != nil {
		return a.putError
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if a.corruptWrite {
		b = []byte("corrupt")
	}
	a.objects[key] = b
	if a.afterPut != nil {
		a.afterPut()
	}
	return nil
}

func TestEnvelopeTrustAndNativeProfile(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*evidenceFixture)
	}{
		{"valid", func(*evidenceFixture) {}},
		{"untrusted key", func(f *evidenceFixture) { f.trust.PublicKey = make(ed25519.PublicKey, ed25519.PublicKeySize) }},
		{"another release", func(f *evidenceFixture) { f.trust.ReleaseID = strings.Repeat("c", 64) }},
		{"another host", func(f *evidenceFixture) { f.trust.HostID = uuid.NewString() }},
		{"another attempt", func(f *evidenceFixture) { f.trust.RunID = uuid.NewString() }},
		{"another commit", func(f *evidenceFixture) { f.trust.SourceCommit = strings.Repeat("c", 40) }},
		{"missing exit", func(f *evidenceFixture) { f.report.TestMetalExitCode = nil; resign(t, f) }},
		{"failed exit", func(f *evidenceFixture) { one := 1; f.report.LeakcheckExitCode = &one; resign(t, f) }},
		{"future completion", func(f *evidenceFixture) { f.report.CompletedAt = time.Now().Add(time.Hour); resign(t, f) }},
		{"zero interval", func(f *evidenceFixture) { f.report.CompletedAt = f.report.StartedAt; resign(t, f) }},
		{"nested host", func(f *evidenceFixture) { f.report.Native.Virtualization = "kvm"; resign(t, f) }},
		{"non native OS", func(f *evidenceFixture) { f.report.Native.OS = "darwin"; resign(t, f) }},
		{"unsupported architecture", func(f *evidenceFixture) { f.report.Native.Architecture = "arm64"; resign(t, f) }},
		{"not retired", func(f *evidenceFixture) { f.report.Native.Retired = false; resign(t, f) }},
		{"missing guest digest", func(f *evidenceFixture) { f.report.Native.GuestInitSHA256 = ""; resign(t, f) }},
		{"missing boot ID", func(f *evidenceFixture) { f.report.Native.KernelBootID = uuid.Nil.String(); resign(t, f) }},
		{"other profile", func(f *evidenceFixture) { f.report.Profile = "scan"; resign(t, f) }},
		{"tampered payload", func(f *evidenceFixture) {
			var e Envelope
			if err := json.Unmarshal(f.raw, &e); err != nil {
				t.Fatal(err)
			}
			e.Payload = base64.StdEncoding.EncodeToString([]byte("{}"))
			f.raw, _ = json.Marshal(e)
		}},
		{"wrong signature domain", func(f *evidenceFixture) {
			var e Envelope
			if err := json.Unmarshal(f.raw, &e); err != nil {
				t.Fatal(err)
			}
			b, _ := base64.StdEncoding.DecodeString(e.Payload)
			e.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(f.key, b))
			f.raw, _ = json.Marshal(e)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEvidenceFixture(t)
			tc.edit(f)
			_, canonical, err := VerifyEnvelope(f.raw, f.trust, time.Now().UTC())
			if tc.name == "valid" {
				if err != nil || !bytes.Equal(canonical, f.raw) {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("invalid native report accepted")
			}
		})
	}
}

func TestSignedJSONRejectsAmbiguousAndOversizedInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{"unknown field", func(b []byte) []byte { return append([]byte(`{"unknown":true,`), b[1:]...) }},
		{"duplicate version", func(b []byte) []byte { return append([]byte(`{"version":1,`), b[1:]...) }},
		{"case duplicate version", func(b []byte) []byte { return append([]byte(`{"Version":1,`), b[1:]...) }},
		{"trailing object", func(b []byte) []byte { return append(b, []byte(` {}`)...) }},
		{"invalid UTF8", func(b []byte) []byte { return append(b, 0xff) }},
		{"oversized", func(b []byte) []byte {
			return append(b, bytes.Repeat([]byte(" "), api.RuntimeQualificationReportMaxBytes)...)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEvidenceFixture(t)
			payload, err := json.Marshal(f.report)
			if err != nil {
				t.Fatal(err)
			}
			f.raw = signUnchecked(t, tc.edit(payload), f.key)
			if _, _, err := VerifyEnvelope(f.raw, f.trust, time.Now()); err == nil {
				t.Fatal("ambiguous signed input accepted")
			}
		})
	}
	nested := strings.Repeat("[", api.RuntimeQualificationJSONMaxDepth+2) + "0" + strings.Repeat("]", api.RuntimeQualificationJSONMaxDepth+2)
	var dst any
	if err := decodeStrict([]byte(nested), &dst); !errors.Is(err, ErrEvidence) {
		t.Fatal("unbounded depth", err)
	}
	for _, n := range []int{3, 4} {
		t.Run(string(rune('0'+n)), func(t *testing.T) {
			_, err := ReadBounded(strings.NewReader(strings.Repeat("a", n)), 3)
			if (err == nil) != (n == 3) {
				t.Fatal(err)
			}
		})
	}
}

func TestDecodeFixtureRequiresExactPublishedGeneration(t *testing.T) {
	f := newEvidenceFixture(t)
	n := f.report.Native
	fixture := Fixture{Target: f.release, RunID: n.RunID, HostID: n.HostID, SourceCommit: n.SourceCommit, DeploymentID: n.DeploymentID, LayerKey: n.LayerKey, LayerSHA256: n.LayerSHA256, KernelSHA256: n.KernelSHA256, FirecrackerSHA256: n.FirecrackerSHA256}
	for _, tc := range []struct {
		name string
		edit func(*Fixture)
	}{{"valid", func(*Fixture) {}}, {"forged identity", func(f *Fixture) { f.Target.BaseSHA256 = strings.Repeat("a", 64) }}, {"unsupported architecture", func(f *Fixture) { f.Target.Architecture = "arm64"; f.Target.ID = f.Target.Identity() }}, {"missing layer digest", func(f *Fixture) { f.LayerSHA256 = "" }}, {"missing run", func(f *Fixture) { f.RunID = "" }}} {
		t.Run(tc.name, func(t *testing.T) {
			copy := fixture
			tc.edit(&copy)
			raw, err := json.Marshal(copy)
			if err != nil {
				t.Fatal(err)
			}
			_, err = DecodeFixture(raw)
			if (err == nil) != (tc.name == "valid") {
				t.Fatal(err)
			}
		})
	}
}
