//go:build unix

package preview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

func secretRuntimeEnv() map[string]string {
	return map[string]string{"FAAS_SECRETS_SNAPSHOT_FILE": "/snapshot", "FAAS_SECRETS_RELOAD_READY_FILE": "/ready",
		"FAAS_SECRETS_RELOAD_ACK_ENDPOINT": "http://metadata.test/ack?workload=worker", "FAAS_SECRETS_RELOAD_GENERATION": strings.Repeat("e", 32)}
}

func secretEnvelope(key string, secrets map[string]string) []byte {
	raw, _ := json.Marshal(map[string]any{"revision": strings.Repeat(key, 64), "secrets": secrets})
	return raw
}

type secretFixture struct {
	mu       sync.Mutex
	raw      []byte
	receipts []map[string]string
	storage  *ObjectStorageBinding[string]
	runtime  *faas.SecretReloadCoordinator
}

func newSecretFixture(t *testing.T, change func(*faas.SecretReloadOptions, *secretFixture)) *secretFixture {
	t.Helper()
	f := &secretFixture{raw: secretEnvelope("a", runtimeStorageValues())}
	f.storage, _ = NewObjectStorageBinding("ASSETS", func(_ context.Context, config ObjectStorageClientConfig) (string, error) {
		return config.SecretAccessKey, nil
	})
	options := faas.SecretReloadOptions{Consumers: []faas.SecretConsumer{f.storage.SecretConsumer()}, Environment: secretRuntimeEnv(), IO: faas.SecretReloadIO{
		Read: func(context.Context, string) ([]byte, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return append([]byte(nil), f.raw...), nil
		},
		Ready: func(context.Context, string) error { return nil },
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
		Post: func(_ context.Context, _ string, raw []byte) (int, error) {
			var body map[string]string
			if err := json.Unmarshal(raw, &body); err != nil {
				return 0, err
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.receipts = append(f.receipts, body)
			return 202, nil
		},
	}}
	if change != nil {
		change(&options, f)
	}
	var err error
	f.runtime, err = faas.NewSecretReloadCoordinator(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.runtime.Stop)
	return f
}

func (f *secretFixture) publish(raw []byte) { f.mu.Lock(); defer f.mu.Unlock(); f.raw = raw }
func (f *secretFixture) acks() []map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]string(nil), f.receipts...)
}

func waitSecret[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case result := <-channel:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("local runtime fixture did not progress")
		var zero T
		return zero
	}
}

func TestSecretReloadManagedLeasesSurvivePartialAdoptionAndStop(t *testing.T) {
	var mu sync.Mutex
	retired := make(map[string]int)
	completed := make(chan string, 3)
	fail := false
	f := newSecretFixture(t, func(options *faas.SecretReloadOptions, f *secretFixture) {
		f.storage, _ = NewObjectStorageBinding("ASSETS", func(_ context.Context, config ObjectStorageClientConfig) (string, error) {
			return config.SecretAccessKey, nil
		}, ObjectStorageBindingOptions[string]{Retire: func(client string) error {
			mu.Lock()
			retired[client]++
			mu.Unlock()
			completed <- client
			return nil
		}})
		options.Consumers = []faas.SecretConsumer{f.storage.SecretConsumer(), {
			Apply: func(context.Context, faas.SecretSnapshot) error {
				if fail {
					return errors.New("private-other-consumer")
				}
				return nil
			}, Invalidate: func() {},
		}}
	})
	if _, err := f.runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	old, err := f.storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer old.Release()
	values := runtimeStorageValues()
	values["ASSETS_SECRET_ACCESS_KEY"] = "private-failed"
	fail = true
	f.publish(secretEnvelope("b", values))
	if _, err := f.runtime.Reload(context.Background()); err == nil {
		t.Fatal("partial adoption succeeded")
	}
	if _, err := f.storage.Acquire(); err == nil {
		t.Fatal("failed adoption still acquired")
	}
	if got := waitSecret(t, completed); got != "private-failed" {
		t.Fatal("wrong rejected client retired")
	}
	if client, _, err := old.Current(); err != nil || client != "private-secret" {
		t.Fatal("old request invalidated")
	}
	acks := f.acks()
	if len(acks) != 2 || acks[0]["status"] != "applied" || acks[1]["status"] != "failed" {
		t.Fatal("partial adoption ACK mismatch")
	}
	fail = false
	values["ASSETS_SECRET_ACCESS_KEY"] = "private-recovered"
	f.publish(secretEnvelope("c", values))
	if _, err := f.runtime.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := f.storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer current.Release()
	f.runtime.Stop()
	if _, err := f.storage.Acquire(); err == nil {
		t.Fatal("stopped coordinator left acquisition enabled")
	}
	mu.Lock()
	before := len(retired)
	mu.Unlock()
	if before != 1 {
		t.Fatal("shutdown closed active leases")
	}
	old.Release()
	current.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := f.storage.WaitForRetirement(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, key := range []string{"private-secret", "private-failed", "private-recovered"} {
		if retired[key] != 1 {
			t.Fatal("coordinator retirement count mismatch")
		}
	}
}

func TestSecretReloadStartupReadinessAndAllConsumers(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	ready, reads := false, 0
	f := newSecretFixture(t, func(options *faas.SecretReloadOptions, f *secretFixture) {
		options.IO.Ready = func(context.Context, string) error { ready = true; return nil }
		options.IO.Read = func(context.Context, string) ([]byte, error) {
			if !ready {
				return nil, errors.New("marker was not written")
			}
			reads++
			if reads == 1 {
				return []byte(`{"revision":"","secrets":{}}`), nil
			}
			return secretEnvelope("a", runtimeStorageValues()), nil
		}
		options.Consumers = append(options.Consumers, faas.SecretConsumer{Apply: func(_ context.Context, snapshot faas.SecretSnapshot) error {
			secrets := snapshot.Secrets()
			secrets["ASSETS_SECRET_ACCESS_KEY"] = "private-mutated"
			if snapshot.Secrets()["ASSETS_SECRET_ACCESS_KEY"] != "private-secret" {
				return errors.New("mutable snapshot")
			}
			raw, _ := json.Marshal(snapshot)
			if strings.Contains(string(raw)+fmt.Sprintf("%+v %#v", snapshot, snapshot), "private-") {
				return errors.New("unsafe snapshot logging")
			}
			close(entered)
			<-release
			return nil
		}, Invalidate: func() {}})
	})
	done := make(chan error, 1)
	go func() { _, err := f.runtime.Start(context.Background()); done <- err }()
	waitSecret(t, entered)
	if len(f.acks()) != 0 {
		t.Fatal("partial adoption acknowledged")
	}
	close(release)
	if err := waitSecret(t, done); err != nil {
		t.Fatal(err)
	}
	if len(f.acks()) != 1 || f.acks()[0]["status"] != "applied" {
		t.Fatal("missing exact ACK")
	}
	if revision, err := f.runtime.Start(context.Background()); err != nil || revision != strings.Repeat("a", 64) {
		t.Fatal("start was not idempotent")
	}
}

func TestSecretReloadRotationRevocationAndPartialFailure(t *testing.T) {
	fail := false
	f := newSecretFixture(t, func(options *faas.SecretReloadOptions, _ *secretFixture) {
		options.Consumers = append(options.Consumers, faas.SecretConsumer{Apply: func(context.Context, faas.SecretSnapshot) error {
			if fail {
				return errors.New("private-driver-error")
			}
			return nil
		}, Invalidate: func() {}})
	})
	if _, err := f.runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	old, _, _ := f.storage.Current()
	values := runtimeStorageValues()
	values["ASSETS_SECRET_ACCESS_KEY"] = "private-next"
	f.publish(secretEnvelope("b", values))
	if _, err := f.runtime.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if current, _, err := f.storage.Current(); err != nil || current != "private-next" || old != "private-secret" {
		t.Fatal("client pair mixed")
	}
	fail = true
	f.publish(secretEnvelope("c", values))
	if _, err := f.runtime.Reload(context.Background()); err == nil || strings.Contains(err.Error(), "private-") {
		t.Fatal("unsafe partial failure")
	}
	if _, _, err := f.storage.Current(); err == nil {
		t.Fatal("partial adoption remained available")
	}
	fail = false
	f.publish(secretEnvelope("d", map[string]string{}))
	if _, err := f.runtime.Reload(context.Background()); err == nil {
		t.Fatal("revocation adopted")
	}
	if len(f.acks()) != 4 || f.acks()[2]["status"] != "failed" || f.acks()[3]["status"] != "failed" {
		t.Fatal("wrong failed ACKs")
	}
	f.publish(secretEnvelope("f", values))
	if _, err := f.runtime.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSecretReloadStaleTransportAndImmutableIdentity(t *testing.T) {
	env := secretRuntimeEnv()
	var bodies [][]byte
	reads := 0
	f := newSecretFixture(t, func(options *faas.SecretReloadOptions, f *secretFixture) {
		options.Environment = env
		options.IO.Read = func(_ context.Context, path string) ([]byte, error) {
			if path != "/snapshot" {
				return nil, errors.New("borrowed path")
			}
			reads++
			if reads > 1 {
				if _, _, err := f.storage.Current(); err == nil {
					return nil, errors.New("stale client still available")
				}
				return secretEnvelope("b", runtimeStorageValues()), nil
			}
			return secretEnvelope("a", runtimeStorageValues()), nil
		}
		options.IO.Post = func(_ context.Context, endpoint string, raw []byte) (int, error) {
			if endpoint != "http://metadata.test/ack?workload=worker" {
				return 0, errors.New("borrowed endpoint")
			}
			bodies = append(bodies, append([]byte(nil), raw...))
			env["FAAS_SECRETS_RELOAD_GENERATION"] = strings.Repeat("f", 32)
			env["FAAS_SECRETS_SNAPSHOT_FILE"] = "/other"
			switch len(bodies) {
			case 1:
				return 0, errors.New("private-network")
			case 2:
				return 503, nil
			case 3:
				return 409, nil
			default:
				return 202, nil
			}
		}
	})
	revision, err := f.runtime.Start(context.Background())
	if err != nil || revision != strings.Repeat("b", 64) {
		t.Fatal("stale retry did not reread")
	}
	if len(bodies) != 4 || string(bodies[0]) != string(bodies[1]) || string(bodies[0]) != string(bodies[2]) {
		t.Fatal("transport body changed")
	}
	for _, raw := range bodies {
		var ack map[string]string
		_ = json.Unmarshal(raw, &ack)
		if len(ack) != 3 || ack["generation"] != strings.Repeat("e", 32) || strings.Contains(string(raw), "private-") {
			t.Fatal("unsafe ACK metadata")
		}
	}
}

func TestSecretReloadInvalidSnapshotsNoACKAndBoundedFailures(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"revision":null,"secrets":{}}`, `{"Revision":"","Secrets":{}}`, `{"revision":"","secrets":null}`, `{"revision":"","secrets":{"key":null}}`, `{"revision":"private-value","secrets":{}}`, `{"revision":"","secrets":{"key":42}}`} {
		t.Run(raw, func(t *testing.T) {
			f := newSecretFixture(t, nil)
			f.publish([]byte(raw))
			if _, err := f.runtime.Start(context.Background()); err == nil || strings.Contains(err.Error(), "private-") {
				t.Fatal("invalid snapshot accepted or leaked")
			}
			if len(f.acks()) != 0 {
				t.Fatal("invalid snapshot acknowledged")
			}
		})
	}
	for _, code := range []int{400, 401, 302, 500, 503, 409} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			calls := 0
			f := newSecretFixture(t, func(options *faas.SecretReloadOptions, _ *secretFixture) {
				options.IO.Post = func(context.Context, string, []byte) (int, error) { calls++; return code, nil }
			})
			if _, err := f.runtime.Start(context.Background()); err == nil {
				t.Fatal("unconfirmed ACK accepted")
			}
			want := 1
			if code == 503 {
				want = 10
			}
			if code == 409 {
				want = 5
			}
			if calls != want {
				t.Fatal("unexpected retry count")
			}
			if _, _, err := f.storage.Current(); err == nil {
				t.Fatal("client available after unconfirmed ACK")
			}
		})
	}
}

func TestSecretReloadENOENTInitialTimeoutAndPanics(t *testing.T) {
	reads := 0
	f := newSecretFixture(t, func(options *faas.SecretReloadOptions, _ *secretFixture) {
		options.IO.Read = func(context.Context, string) ([]byte, error) {
			reads++
			if reads < 3 {
				return nil, os.ErrNotExist
			}
			return secretEnvelope("a", runtimeStorageValues()), nil
		}
	})
	if _, err := f.runtime.Start(context.Background()); err != nil || reads != 3 {
		t.Fatal("ENOENT was not retried")
	}
	for _, phase := range []string{"pending", "ready", "apply", "invalidate"} {
		t.Run(phase, func(t *testing.T) {
			f := newSecretFixture(t, func(options *faas.SecretReloadOptions, _ *secretFixture) {
				switch phase {
				case "pending":
					options.StartupTimeout = 10 * time.Millisecond
					options.IO.Sleep = nil
					options.IO.Read = func(context.Context, string) ([]byte, error) { return []byte(`{"revision":"","secrets":{}}`), nil }
				case "ready":
					options.IO.Ready = func(context.Context, string) error { panic("private-ready") }
				case "apply":
					options.Consumers = append(options.Consumers, faas.SecretConsumer{Apply: func(context.Context, faas.SecretSnapshot) error { panic("private-consumer") }, Invalidate: func() {}})
				case "invalidate":
					options.Consumers = append([]faas.SecretConsumer{{Apply: func(context.Context, faas.SecretSnapshot) error { return nil }, Invalidate: func() { panic("private-invalidation") }}}, options.Consumers...)
				}
			})
			if _, err := f.runtime.Start(context.Background()); err == nil || strings.Contains(err.Error(), "private-") {
				t.Fatal("unsafe startup failure")
			}
			if _, _, err := f.storage.Current(); err == nil {
				t.Fatal("failure kept client available")
			}
			want := 0
			if phase == "apply" {
				want = 1
			}
			if len(f.acks()) != want || want == 1 && f.acks()[0]["status"] != "failed" {
				t.Fatal("wrong startup ACK")
			}
		})
	}
}

func TestSecretReloadCancellationFencesLateFactoryAndQueuedRequests(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	f := newSecretFixture(t, func(options *faas.SecretReloadOptions, f *secretFixture) {
		f.storage, _ = NewObjectStorageBinding("ASSETS", func(context.Context, ObjectStorageClientConfig) (string, error) {
			calls++
			if calls == 2 {
				close(entered)
				<-release
			}
			return "candidate", nil
		})
		options.Consumers = []faas.SecretConsumer{f.storage.SecretConsumer()}
	})
	if _, err := f.runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := f.runtime.Reload(ctx); done <- err }()
	waitSecret(t, entered)
	queued := make(chan error, 1)
	go func() { _, err := f.runtime.Reload(context.Background()); queued <- err }()
	cancel()
	if err := waitSecret(t, done); err == nil {
		t.Fatal("cancelled reload passed")
	}
	if err := waitSecret(t, queued); err == nil {
		t.Fatal("queued reload passed shutdown")
	}
	if _, _, err := f.storage.Current(); err == nil {
		t.Fatal("shutdown blocked invalidation")
	}
	// Stop fences consumers promptly; wait for the cancelled worker's final
	// invalidation before starting an independent reload. The factory is still
	// blocked here, so coordinator shutdown must not depend on its completion.
	waitSecret(t, f.runtime.Done())
	close(release)
	// A fresh factory can only run after the late one drains; it cannot overlap.
	if err := f.storage.Reload(context.Background(), runtimeStorageValues()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(f.acks()) != 1 {
		t.Fatal("late factory acknowledged or overlapped")
	}
}

func TestObjectStorageInvalidationFencesLateAndQueuedFactories(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	b, _ := NewObjectStorageBinding("ASSETS", func(_ context.Context, c ObjectStorageClientConfig) (string, error) {
		if c.SecretAccessKey == "private-late" {
			close(entered)
			<-release
		}
		return c.SecretAccessKey, nil
	})
	values := runtimeStorageValues()
	values["ASSETS_SECRET_ACCESS_KEY"] = "private-late"
	late := make(chan error, 1)
	go func() { late <- b.Reload(context.Background(), values) }()
	waitSecret(t, entered)
	b.Invalidate()
	if _, _, err := b.Current(); err == nil {
		t.Fatal("invalidation waited for factory")
	}
	close(release)
	if err := waitSecret(t, late); err == nil {
		t.Fatal("late candidate published")
	}
	if err := b.Reload(context.Background(), runtimeStorageValues()); err != nil {
		t.Fatal(err)
	}
}

func TestSecretReloadContractValidation(t *testing.T) {
	consumer := faas.SecretConsumer{Apply: func(context.Context, faas.SecretSnapshot) error { return nil }, Invalidate: func() {}}
	for _, input := range []struct{ key, value string }{{"FAAS_SECRETS_SNAPSHOT_FILE", "relative"}, {"FAAS_SECRETS_RELOAD_READY_FILE", ""}, {"FAAS_SECRETS_RELOAD_GENERATION", ""}, {"FAAS_SECRETS_RELOAD_ACK_ENDPOINT", "https://user:private@host.test/ack"}} {
		env := secretRuntimeEnv()
		env[input.key] = input.value
		if _, err := faas.NewSecretReloadCoordinator(faas.SecretReloadOptions{Consumers: []faas.SecretConsumer{consumer}, Environment: env}); err == nil || strings.Contains(err.Error(), "private-") {
			t.Fatal("unsafe contract validation")
		}
	}
}

// Run the actual SDK in an isolated test process so signal registration/cleanup
// and HTTP/file I/O are qualified without sending signals to the test runner.
func TestSecretReloadChildProcess(t *testing.T) {
	if os.Getenv("GREGALE_SECRET_RELOAD_CHILD") != "1" {
		t.Skip("child-process entry point")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	b, _ := NewObjectStorageBinding("ASSETS", func(_ context.Context, config ObjectStorageClientConfig) (string, error) {
		return config.SecretAccessKey, nil
	})
	c, err := faas.NewSecretReloadCoordinator(faas.SecretReloadOptions{Consumers: []faas.SecretConsumer{b.SecretConsumer()}})
	if err != nil {
		t.Fatal("runtime construction failed")
	}
	if _, err := c.Start(ctx); err != nil {
		t.Fatal("runtime startup failed")
	}
	<-ctx.Done()
	c.Stop()
	if _, _, err := b.Current(); err == nil {
		t.Fatal("shutdown left client available")
	}
}

func TestSecretReloadRealFilesHTTPAndSIGHUP(t *testing.T) {
	directory := t.TempDir()
	snapshot, ready := filepath.Join(directory, "snapshot"), filepath.Join(directory, "ready")
	publish := func(key string, secrets map[string]string) {
		t.Helper()
		next := filepath.Join(directory, "next")
		if err := os.WriteFile(next, secretEnvelope(key, secrets), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(next, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	publish("a", runtimeStorageValues())
	acks := make(chan map[string]string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != "POST" || request.URL.String() != "/ack?workload=worker" || request.Header.Get("Authorization") != "" {
			w.WriteHeader(400)
			return
		}
		raw, _ := io.ReadAll(request.Body)
		var ack map[string]string
		if json.Unmarshal(raw, &ack) != nil || len(ack) != 3 || ack["generation"] != strings.Repeat("e", 32) {
			w.WriteHeader(400)
			return
		}
		acks <- ack
		w.WriteHeader(202)
	}))
	defer server.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestSecretReloadChildProcess$")
	command.Env = append(os.Environ(), "GREGALE_SECRET_RELOAD_CHILD=1", "FAAS_SECRETS_SNAPSHOT_FILE="+snapshot, "FAAS_SECRETS_RELOAD_READY_FILE="+ready,
		"FAAS_SECRETS_RELOAD_ACK_ENDPOINT="+server.URL+"/ack?workload=worker", "FAAS_SECRETS_RELOAD_GENERATION="+strings.Repeat("e", 32), "HTTP_PROXY=http://127.0.0.1:1")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	first := waitSecret(t, acks)
	if first["revision"] != strings.Repeat("a", 64) || first["status"] != "applied" {
		t.Fatal("startup ACK mismatch")
	}
	mode, err := os.Stat(ready)
	if err != nil || mode.Mode().Perm() != 0o600 {
		t.Fatal("readiness file mismatch")
	}
	publish("b", runtimeStorageValues())
	if err := command.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	if ack := waitSecret(t, acks); ack["revision"] != strings.Repeat("b", 64) || ack["status"] != "applied" {
		t.Fatal("rotation ACK mismatch")
	}
	publish("c", map[string]string{})
	_ = command.Process.Signal(syscall.SIGHUP)
	if ack := waitSecret(t, acks); ack["revision"] != strings.Repeat("c", 64) || ack["status"] != "failed" {
		t.Fatal("revocation ACK mismatch")
	}
	publish("d", runtimeStorageValues())
	_ = command.Process.Signal(syscall.SIGHUP)
	if ack := waitSecret(t, acks); ack["revision"] != strings.Repeat("d", 64) || ack["status"] != "applied" {
		t.Fatal("recovery ACK mismatch")
	}
	_ = command.Process.Signal(syscall.SIGTERM)
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	if err := waitSecret(t, finished); err != nil {
		t.Fatal("child workload failed")
	}
}
