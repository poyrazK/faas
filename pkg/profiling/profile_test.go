package profiling

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
)

func cpuFixture(now time.Time, cpu int64) *profile.Profile {
	leaf := &profile.Function{ID: 1, Name: "hot", Filename: "app.go"}
	caller := &profile.Function{ID: 2, Name: "handler", Filename: "app.go"}
	a := &profile.Location{ID: 1, Line: []profile.Line{{Function: leaf, Line: 12}}}
	b := &profile.Location{ID: 2, Line: []profile.Line{{Function: caller, Line: 4}}}
	return &profile.Profile{SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}}, PeriodType: &profile.ValueType{Type: "cpu", Unit: "nanoseconds"}, Period: 10000000, TimeNanos: now.UnixNano(), DurationNanos: int64(time.Second), Function: []*profile.Function{leaf, caller}, Location: []*profile.Location{a, b}, Sample: []*profile.Sample{{Location: []*profile.Location{a, b}, Value: []int64{cpu}, Label: map[string][]string{"account_id": {"spoofed"}}}}}
}

func encoded(t *testing.T, p *profile.Profile) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := p.Write(&b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestCPUProfileDeploymentComparison(t *testing.T) {
	now := time.Now()
	q := api.ProfileQuery{DeploymentID: uuid.NewString(), Runtime: "go124", Start: now, End: now.Add(10 * time.Second)}
	a, err := View(cpuFixture(now, 1e9), q)
	if err != nil {
		t.Fatal(err)
	}
	q.DeploymentID = uuid.NewString()
	q.End = now.Add(20 * time.Second)
	b, err := View(cpuFixture(now, 4e9), q)
	if err != nil {
		t.Fatal(err)
	}
	out := Compare(a, b)
	if !out.Comparable || len(out.Functions) != 2 || math.Abs(out.Functions[0].DeltaCPUPerSecond-.1) > 1e-9 {
		t.Fatalf("comparison: %+v", out)
	}
	if a.Functions[0].Name != "hot" || a.Functions[0].SelfCPUSeconds != 1 || a.Flamegraph.Children[0].Name != "handler" || a.Flamegraph.Children[0].Children[0].Name != "hot" {
		t.Fatalf("call paths: %+v", a)
	}
	empty, _ := View(nil, q)
	if Compare(empty, b).Comparable || !empty.Empty {
		t.Fatal("missing profiles must not compare as zero CPU")
	}
	b.Query.Runtime = "python312"
	if Compare(a, b).Comparable {
		t.Fatal("different runtimes must not compare")
	}
}

func TestCPUProfileBoundsAndWallRejection(t *testing.T) {
	p := cpuFixture(time.Now(), 1)
	p.SampleType[0].Type = "wall"
	if NormalizeCPU(p) == nil {
		t.Fatal("wall time presented as CPU")
	}
	p = cpuFixture(time.Now(), -1)
	if NormalizeCPU(p) == nil {
		t.Fatal("negative CPU accepted")
	}
	var bomb bytes.Buffer
	gz := gzip.NewWriter(&bomb)
	_, _ = gz.Write(bytes.Repeat([]byte{0}, api.ProfileMaxExpandedBytes+1))
	_ = gz.Close()
	if _, err := Parse(bomb.Bytes()); err == nil {
		t.Fatal("expansion bomb accepted")
	}
	var nested bytes.Buffer
	gz = gzip.NewWriter(&nested)
	_, _ = gz.Write(bomb.Bytes())
	_ = gz.Close()
	if _, err := Parse(nested.Bytes()); err == nil {
		t.Fatal("nested compression accepted")
	}
	if _, err := Parse(bytes.Repeat([]byte{0}, api.ProfileMaxCompressedBytes+1)); err == nil {
		t.Fatal("oversized upload accepted")
	}
}

type captureBackend struct {
	mu               sync.Mutex
	pushes           int
	principal        Principal
	p                *profile.Profile
	fail             bool
	entered, release chan struct{}
}

func (b *captureBackend) Push(ctx context.Context, principal Principal, p *profile.Profile) error {
	if b.entered != nil {
		select {
		case b.entered <- struct{}{}:
		default:
		}
		select {
		case <-b.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		return fmt.Errorf("unavailable")
	}
	b.pushes++
	b.principal = principal
	b.p = p
	return nil
}
func (*captureBackend) Query(context.Context, string, string, string, api.ProfileQuery) (*profile.Profile, error) {
	return nil, nil
}
func principalFixture(now time.Time) Principal {
	return Principal{AccountID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), InstanceID: uuid.NewString(), Generation: "1", Scope: api.DefaultEnvScope, Runtime: "go124", Plan: api.PlanHobby, StartedAt: now.Add(-time.Second)}
}

func TestCPUProfileIngestionIdentityLifetimeAndRetries(t *testing.T) {
	now := time.Now().Add(-time.Second)
	backend := &captureBackend{}
	service := NewService(backend, nil)
	e := Envelope{Principal: principalFixture(now), Upload: Upload{Profile: encoded(t, cpuFixture(now, 1e8))}}
	if err := service.Push(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(backend.principal, e.Principal) || len(backend.p.Sample[0].Label) != 0 {
		t.Fatal("guest labels escaped trusted metadata boundary")
	}
	if err := service.Push(context.Background(), e); err != nil || backend.pushes != 1 {
		t.Fatal("retry exported twice", err)
	}
	e.Principal.StartedAt = now.Add(time.Millisecond)
	if status.Code(service.Push(context.Background(), e)) != codes.InvalidArgument {
		t.Fatal("pre-restore samples accepted")
	}
	e.Principal.StartedAt = now.Add(-time.Second)
	e.Principal.Plan = api.PlanFree
	if status.Code(service.Push(context.Background(), e)) != codes.PermissionDenied {
		t.Fatal("free upload accepted")
	}
	e.Principal.Plan = api.PlanHobby
	e.Principal.AccountID = "spoofed"
	if status.Code(service.Push(context.Background(), e)) != codes.InvalidArgument {
		t.Fatal("invalid principal accepted")
	}
}

func TestCPUProfileDistinctProcessesAndRetryIdentity(t *testing.T) {
	now := time.Now().Add(-time.Second)
	var mu sync.Mutex
	var ids []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		series, err := profilePushMessages(body, 1)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		samples, err := profilePushMessages(series[0], 2)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		rawIDs, err := profilePushMessages(samples[0], 2)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		ids = append(ids, string(rawIDs[0]))
		mu.Unlock()
		if bytes.Contains(body, []byte("gregale_process")) {
			t.Error("guest process discriminator became a backend label")
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	backend, err := NewPyroscope(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(backend, nil)
	e := Envelope{Principal: principalFixture(now), Upload: Upload{Profile: encoded(t, cpuFixture(now, 1e8)), ProcessID: "42"}}
	if err := service.Push(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	e.Upload.ProcessID = "43"
	if err := service.Push(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if err := service.Push(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("distinct workers collapsed or retry duplicated: %v", ids)
	}
}

func profilePushMessages(body []byte, field protowire.Number) ([][]byte, error) {
	var values [][]byte
	for len(body) > 0 {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			return nil, fmt.Errorf("invalid tag")
		}
		body = body[n:]
		if num == field && typ == protowire.BytesType {
			value, n := protowire.ConsumeBytes(body)
			if n < 0 {
				return nil, fmt.Errorf("invalid bytes")
			}
			values = append(values, value)
			body = body[n:]
		} else {
			n := protowire.ConsumeFieldValue(num, typ, body)
			if n < 0 {
				return nil, fmt.Errorf("invalid field")
			}
			body = body[n:]
		}
	}
	return values, nil
}

func TestCPUProfileUnavailableRetryAndConcurrentDedup(t *testing.T) {
	now := time.Now().Add(-time.Second)
	backend := &captureBackend{fail: true}
	service := NewService(backend, nil)
	e := Envelope{Principal: principalFixture(now), Upload: Upload{Profile: encoded(t, cpuFixture(now, 1e8))}}
	if status.Code(service.Push(context.Background(), e)) != codes.Unavailable {
		t.Fatal("backend outage acknowledged")
	}
	backend.fail = false
	backend.entered = make(chan struct{}, 1)
	backend.release = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- service.Push(context.Background(), e) }()
	<-backend.entered
	if status.Code(service.Push(context.Background(), e)) != codes.Unavailable {
		t.Fatal("inflight retry acknowledged before export")
	}
	close(backend.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := service.Push(context.Background(), e); err != nil || backend.pushes != 1 {
		t.Fatal("failed retry not recoverable", err)
	}
}

func TestCPUProfileBackendTenantSelectorsAndProtocol(t *testing.T) {
	now := time.Now().Add(-time.Minute)
	principal := principalFixture(now)
	q := api.ProfileQuery{DeploymentID: principal.DeploymentID, Runtime: "go124", Start: now, End: now.Add(time.Second)}
	seen := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		if r.Header.Get("X-Scope-OrgID") != principal.AccountID || r.Header.Get("Authorization") != "Bearer operator-token" {
			t.Error("trusted tenant header missing")
		}
		if r.URL.Path == "/push.v1.PusherService/Push" {
			raw, _ := io.ReadAll(r.Body)
			if !bytes.Contains(raw, []byte(principal.AppID)) || !bytes.Contains(raw, []byte(principal.DeploymentID)) || bytes.Contains(raw, []byte("spoofed")) {
				t.Error("invalid owned push labels")
			}
			if _, err := profileproto.DecodePush(raw, false); err != nil {
				t.Error("invalid push protobuf", err)
			}
			w.WriteHeader(200)
			return
		}
		if r.URL.Path != "/querier.v1.QuerierService/SelectMergeProfile" || r.Header.Get("Content-Type") != "application/proto" {
			t.Error("wrong query protocol")
		}
		raw, _ := io.ReadAll(r.Body)
		fields := map[protowire.Number]string{}
		for len(raw) > 0 {
			num, typ, n := protowire.ConsumeTag(raw)
			if n < 0 {
				t.Fatal("invalid protobuf")
			}
			raw = raw[n:]
			if typ == protowire.BytesType {
				v, n := protowire.ConsumeBytes(raw)
				fields[num] = string(v)
				raw = raw[n:]
			} else {
				_, n := protowire.ConsumeVarint(raw)
				raw = raw[n:]
			}
		}
		if fields[1] != CPUProfileType || !strings.Contains(fields[2], principal.AppID) || !strings.Contains(fields[2], principal.DeploymentID) || !strings.Contains(fields[2], principal.Scope) {
			t.Error("query escaped owned selector", fields)
		}
		var body bytes.Buffer
		_ = cpuFixture(now, 1e8).WriteUncompressed(&body)
		_, _ = w.Write(body.Bytes())
	}))
	defer server.Close()
	backend, err := NewPyroscope(server.URL, "operator-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Push(context.Background(), principal, cpuFixture(now, 1e8)); err != nil {
		t.Fatal(err)
	}
	p, err := backend.Query(context.Background(), principal.AccountID, principal.AppID, principal.Scope, q)
	if err != nil || p == nil || seen != 2 {
		t.Fatal("query failed", err)
	}
}

// SDK smoke captures are produced by tests/profiling/sdk_smoke.py. This test
// qualifies their encoding and actual CPU units, rather than a hand-made mock.
func TestCPUProfileSDKCaptures(t *testing.T) {
	dir := os.Getenv("GREGALE_PROFILE_SDK_CAPTURES")
	if dir == "" {
		t.Skip("run the native SDK smoke fixture first")
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.bin"))
	if err != nil || len(paths) == 0 {
		t.Fatal("no SDK captures", err)
	}
	processLabel := regexp.MustCompile(`gregale_process="?([0-9]+)"?`)
	pythonProcesses := map[string]bool{}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var meta struct {
				Headers map[string]string
				Query   map[string][]string
			}
			blob, _ := os.ReadFile(strings.TrimSuffix(path, ".bin") + ".json")
			if err := json.Unmarshal(blob, &meta); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(strings.Join(meta.Query["capture_path"], ""), "PusherService") {
				items, err := profileproto.DecodePush(raw, meta.Headers["content-encoding"] == "gzip")
				if err != nil {
					t.Fatal(err)
				}
				if len(items) != 1 || items[0].Epoch != strings.Repeat("1", 32) {
					t.Fatal("SDK epoch missing")
				}
				if items[0].Upload.ProcessID == "" || items[0].Upload.ProcessID == "0" {
					t.Fatal("Python collector process identity missing")
				}
				pythonProcesses[items[0].Upload.ProcessID] = true
				raw = items[0].Upload.Profile
			} else if match := processLabel.FindStringSubmatch(strings.Join(meta.Query["name"], "")); len(match) != 2 || match[1] == "0" {
				t.Fatal("Node collector process identity missing")
			}
			if strings.HasPrefix(meta.Headers["Content-Type"], "multipart/") {
				req := httptest.NewRequest("POST", "/ingest", bytes.NewReader(raw))
				req.Header.Set("Content-Type", meta.Headers["Content-Type"])
				reader, err := req.MultipartReader()
				if err != nil {
					t.Fatal(err)
				}
				for {
					part, err := reader.NextPart()
					if err != nil {
						t.Fatal("no profile multipart", err)
					}
					if part.FormName() == "profile" {
						raw, err = io.ReadAll(part)
						if err != nil {
							t.Fatal(err)
						}
						break
					}
				}
			}
			p, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if err := NormalizeCPU(p); err != nil {
				t.Fatal(err, p.SampleType)
			}
			view, err := View(p, api.ProfileQuery{})
			if err != nil || view.Empty || view.CPUSeconds <= 0 {
				t.Fatal("SDK capture lacks actual CPU", err)
			}
		})
	}
	if os.Getenv("GREGALE_PROFILE_SDK_FORK") == "1" && len(pythonProcesses) < 2 {
		t.Fatal("fork fixture did not capture CPU from both Python workers")
	}
}
