//go:build linux

package objectqualification

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func TestMeasurementCountsErrorsAndRejectionsSeparately(t *testing.T) {
	m := &measurements{}
	start := time.Now()
	m.record("get", start, 10, false, nil)
	m.record("get", start, 0, true, nil)
	m.record("get", start, 999, false, context.DeadlineExceeded)
	report := m.finish(0, start, 1, "duration")
	s := report.Operations["get"]
	if s.Attempts != 3 || s.Successes != 1 || s.ExpectedRejections != 1 || s.Failures != 1 || s.PayloadBytes != 10 || s.P50MS > s.P95MS || s.P95MS > s.MaxMS {
		t.Fatal(report)
	}
	if percentile([]float64{1, 2, 3, 4}, .95) != 4 || percentile(nil, .5) != 0 {
		t.Fatal("wrong nearest-rank quantiles")
	}
}

func TestQualificationUsesDistinctListenerPorts(t *testing.T) {
	for range 16 {
		service, metrics, err := loopbackAddresses()
		if err != nil || service == metrics {
			t.Fatal("listener reservations overlap", service, metrics, err)
		}
	}
}

func TestQualificationRefusesExistingDataAndReportReplacement(t *testing.T) {
	o := DefaultOptions()
	o.Daemon, o.Control = "/bin/true", "/bin/true"
	o.Directory = t.TempDir()
	marker := filepath.Join(o.Directory, "user-data")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := Run(t.Context(), o)
	if err == nil || report.Passed {
		t.Fatal("existing data was admitted", report, err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "keep" {
		t.Fatal("existing data was changed", err)
	}
	path := filepath.Join(t.TempDir(), "report.json")
	if err = WriteReport(path, report); err != nil {
		t.Fatal(err)
	}
	if err = WriteReport(path, Report{Passed: true}); err == nil {
		t.Fatal("report was replaced")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("report permissions", info, err)
	}
	data, err = os.ReadFile(path)
	var saved Report
	if err != nil || json.Unmarshal(data, &saved) != nil || saved.Passed || saved.Error == "" {
		t.Fatal("failure report was not retained", string(data), err)
	}
}

func TestQualificationBoundsAndReproduciblePayloads(t *testing.T) {
	base := DefaultOptions()
	base.Daemon, base.Control = "/bin/true", "/bin/true"
	base.Directory = filepath.Join(t.TempDir(), "new")
	for _, mutate := range []func(*Options){func(o *Options) { o.Workers = api.ObjectQualificationMaxWorkers + 1 }, func(o *Options) { o.Cycles = 0 }, func(o *Options) { o.Duration = api.ObjectQualificationMaxDuration + 1 }, func(o *Options) { o.ObjectBytes = api.ObjectQualificationMaxBytes + 1 }, func(o *Options) { o.MaxWorkflows = api.ObjectQualificationMaxWorkflows + 1 }, func(o *Options) { o.Durability = "force" }} {
		o := base
		mutate(&o)
		if err := o.validate(); err == nil {
			t.Fatal("unbounded configuration was accepted", o)
		}
	}
	a, b := expectation(payload(1, "key", 123), "etag"), expectation(payload(1, "key", 123), "etag")
	if *a != *b || a.SHA256 == expectation(payload(2, "key", 123), "etag").SHA256 {
		t.Fatal("payload seed was ignored")
	}
}

func TestFaultProxyPartitionsOnlyReplicationAndDropsOneResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "durable") }))
	defer upstream.Close()
	p := newFaultProxy()
	defer p.server.Close()
	defer p.transport.CloseIdleConnections()
	p.target.Store(upstream.URL)
	p.partition.Store(true)
	get := func(path string) int {
		t.Helper()
		r, err := http.Get(p.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		_, _ = io.Copy(io.Discard, r.Body)
		return r.StatusCode
	}
	if get("/?"+objectnode.ReplicationQuery+"=status") != 503 || get("/physical/key") != 200 {
		t.Fatal("replication fault affected ordinary reads")
	}
	p.dropResponse.Store(true)
	for _, want := range []int{502, 200} {
		r, err := http.NewRequestWithContext(t.Context(), http.MethodPut, p.server.URL+"/physical/key", strings.NewReader("body"))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set(objectnode.ReceiptMetadataHeader, "captured")
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != want {
			t.Fatal(response.StatusCode, want)
		}
	}
	if p.puts.Load() != 2 || p.lastReceipt.Load().(string) != "captured" {
		t.Fatal("fault proxy lost dispatch identity")
	}
}

// Opt-in uses the exact built executables being qualified, and always creates
// fresh data. The normal unit suite does not build nested binaries implicitly.
func TestQualificationBuiltProcesses(t *testing.T) {
	daemon, control := os.Getenv("FAAS_OBJECT_QUALIFICATION_DAEMON"), os.Getenv("FAAS_OBJECT_QUALIFICATION_CTL")
	if daemon == "" || control == "" {
		t.Skip("set FAAS_OBJECT_QUALIFICATION_DAEMON and FAAS_OBJECT_QUALIFICATION_CTL to built executables")
	}
	o := DefaultOptions()
	o.Daemon, o.Control = daemon, control
	o.Directory = filepath.Join(t.TempDir(), "qualification")
	o.Workers = 2
	o.MaxWorkflows = 4
	o.Cycles = 1
	o.Duration = time.Second
	o.ObjectBytes = 1024
	report, err := Run(t.Context(), o)
	if err != nil || !report.Passed || len(report.Runs) != 2 {
		body, _ := json.MarshalIndent(report, "", "  ")
		t.Fatal(string(body), err)
	}
	for _, mode := range report.Runs {
		if !mode.Passed || len(mode.Checks) != 12 || len(mode.Loads) != 1 || mode.Resources.Samples == 0 || mode.Resources.PrimaryRSSPeak == 0 || mode.Resources.FollowerRSSPeak == 0 {
			t.Fatal("incomplete qualification coverage", mode)
		}
		for _, operation := range []string{"put", "get", "delete", "multipart_create", "multipart_part", "multipart_complete"} {
			stats, ok := mode.Loads[0].Operations[operation]
			if !ok || stats.Attempts == 0 || stats.Failures != 0 {
				t.Fatal("workload coverage missing", operation, stats)
			}
		}
	}
}

func TestQualificationChildHelper(t *testing.T) {
	path := os.Getenv("FAAS_QUALIFICATION_TEST_READY")
	if path == "" {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	if err := os.WriteFile(path, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
}

func TestQualificationChildLifecycleAndCredentialIsolation(t *testing.T) {
	flags := []string{objectstorage.OwnedNodeEnabledEnv, objectstorage.OwnedNodeServerEnabledEnv, objectstorage.OwnedNodeBackendsEnabledEnv, objectstorage.OwnedNodeSettlementsEnabledEnv, objectstorage.OwnedNodeUsageCountersEnabledEnv}
	for _, kill := range []bool{false, true} {
		for _, label := range []string{"primary", "follower"} {
			ready := filepath.Join(t.TempDir(), "ready")
			t.Setenv("FAAS_QUALIFICATION_TEST_READY", ready)
			t.Setenv("FAAS_OBJECT_NODE_ACCESS_KEY_ID", "inherited-key")
			t.Setenv("FAAS_OBJECT_NODE_SECRET_ACCESS_KEY", "inherited-secret")
			for _, name := range flags {
				t.Setenv(name, "inherited-disabled")
			}
			l := &lab{root: t.TempDir(), key: "isolated-key", secret: "isolated-secret"}
			c, err := l.spawn(t.Context(), os.Args[0], label, "-test.run=^TestQualificationChildHelper$")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stopChild(c, true) }()
			wait, cancel := context.WithTimeout(t.Context(), api.ObjectQualificationStartupTimeout)
			err = poll(wait, func() (bool, error) { _, err := os.Stat(ready); return err == nil, nil })
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{}
			for _, item := range c.command.Env {
				if strings.HasPrefix(item, "FAAS_OBJECT_NODE_") {
					counts[strings.SplitN(item, "=", 2)[0]]++
					if strings.Contains(item, "inherited") {
						t.Fatal("node inherited external credentials")
					}
				}
			}
			if counts["FAAS_OBJECT_NODE_ACCESS_KEY_ID"] != 1 || counts["FAAS_OBJECT_NODE_SECRET_ACCESS_KEY"] != 1 {
				t.Fatal("missing or duplicate node credentials", counts)
			}
			for _, name := range flags {
				want := 0
				if label == "primary" && (name == objectstorage.OwnedNodeEnabledEnv || name == objectstorage.OwnedNodeServerEnabledEnv) {
					want = 1
				}
				if counts[name] != want {
					t.Fatal("child received an unnecessary or duplicate feature gate", label, name, counts)
				}
				if os.Getenv(name) != "inherited-disabled" {
					t.Fatal("qualification changed a parent development gate", name)
				}
			}
			if err = stopChild(c, kill); err != nil {
				t.Fatal(err)
			}
			select {
			case <-c.done:
			default:
				t.Fatal("process was not reaped")
			}
		}
	}
}

func TestQualificationStripsAllPrivateFeaturesFromRemoteEnvironment(t *testing.T) {
	env := []string{"PATH=/bin", "FAAS_OBJECT_NODE_ACCESS_KEY_ID=inherited", "FAAS_OBJECT_NODE_SECRET_ACCESS_KEY=inherited", "FAAS_OBJECT_NODE_ENABLED=1", "FAAS_OBJECT_NODE_SERVER_ENABLED=1", "FAAS_OBJECT_NODE_BACKENDS_ENABLED=1", "FAAS_OBJECT_NODE_SETTLEMENTS_ENABLED=1", "FAAS_OBJECT_NODE_USAGE_COUNTERS_ENABLED=1", "FAAS_OBJECT_NODE_ENABLED_EXTRA=keep"}
	got := withoutNodeCredentials(env)
	if strings.Join(got, "\n") != "PATH=/bin\nFAAS_OBJECT_NODE_ENABLED_EXTRA=keep" {
		t.Fatal("remote command inherited private feature flags", got)
	}
}
