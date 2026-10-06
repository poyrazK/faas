//go:build linux

package main

import (
	"bufio"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRuntimeSecretReloadChild(t *testing.T) {
	if os.Getenv("GREGale_RELOAD_CHILD") != "1" {
		return
	}
	println("started")
	for {
		if _, err := os.Stat(os.Getenv("GREGale_RELOAD_RELEASE")); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	received := make(chan os.Signal, 1)
	signal.Notify(received, syscall.SIGHUP)
	defer signal.Stop(received)
	if err := os.WriteFile(os.Getenv(SecretsReloadReadyEnv), []byte("ready\n"), 0600); err != nil {
		t.Fatal(err)
	}
	println("ready")
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("reload signal was not received")
	}
}

func TestRuntimeSecretReloadWaitsForHandler(t *testing.T) {
	dir := t.TempDir()
	ready, release := filepath.Join(dir, "ready"), filepath.Join(dir, "release")
	if err := os.WriteFile(ready, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeSecretReloadChild$")
	cmd.Env = append(os.Environ(), "GREGale_RELOAD_CHILD=1", "GREGale_RELOAD_RELEASE="+release, SecretsReloadReadyEnv+"="+ready)
	output, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	sup := &Supervisor{}
	sup.trackRuntimeSecretCommand(cmd, runtimeSecretSnapshot{Revision: strings.Repeat("a", 64), Secrets: map[string]string{"DB": "old"}}, ready)
	if status, err := sup.deliverRuntimeSecretReload(syscall.SIGHUP, map[string]string{"DB": "new"}, sendRuntimeSecretSignal); err != nil || status != "queued" {
		t.Fatalf("before fork: %s %v", status, err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	sup.markStarted() // production order: immediately after Start, no sleep.
	sup.markHealthy()
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() || scanner.Text() != "started" {
		t.Fatal("child did not reach delayed initialization")
	}
	for i := 0; i < 3; i++ {
		if status, err := sup.deliverRuntimeSecretReload(syscall.SIGHUP, map[string]string{"DB": "new"}, sendRuntimeSecretSignal); err != nil || status != "queued" {
			t.Fatalf("before handler: %s %v", status, err)
		}
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if !scanner.Scan() || scanner.Text() != "ready" {
		t.Fatal("child did not install its handler")
	}
	if status, err := sup.deliverRuntimeSecretReload(syscall.SIGHUP, map[string]string{"DB": "new"}, sendRuntimeSecretSignal); err != nil || status != "sent" {
		t.Fatalf("after handler: %s %v", status, err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child did not apply its signal handler: %v", err)
	}
	sup.retireRuntimeSecretCommand(cmd)
}

func reloadTestSupervisor(t *testing.T, values map[string]string) (*Supervisor, string) {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	if err := os.WriteFile(ready, nil, 0600); err != nil {
		t.Fatal(err)
	}
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Release() })
	sup := &Supervisor{}
	sup.trackRuntimeSecretCommand(&exec.Cmd{Process: process}, runtimeSecretSnapshot{Revision: strings.Repeat("a", 64), Secrets: values}, ready)
	sup.markStarted()
	sup.markHealthy()
	return sup, ready
}

func TestRuntimeSecretReloadStartupAndRestart(t *testing.T) {
	sup, ready := reloadTestSupervisor(t, map[string]string{"DB": "current"})
	sent := 0
	send := func(*os.Process, syscall.Signal) error { sent++; return nil }
	if status, err := sup.deliverRuntimeSecretReload(syscall.SIGHUP, map[string]string{"DB": "current"}, send); err != nil || status != "not_attempted" || sent != 0 {
		t.Fatalf("redundant startup notification: %s %v sent=%d", status, err, sent)
	}
	if err := os.WriteFile(ready, []byte("ready\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"rotated", "current"} {
		if status, err := sup.deliverRuntimeSecretReload(syscall.SIGHUP, map[string]string{"DB": value}, send); err != nil || status != "sent" {
			t.Fatalf("rotation/revert: %s %v", status, err)
		}
	}
	previous := sup.runtimeSecretStart.cmd
	newReady := filepath.Join(t.TempDir(), "next-ready")
	if err := os.WriteFile(newReady, nil, 0600); err != nil {
		t.Fatal(err)
	}
	sup.trackRuntimeSecretCommand(&exec.Cmd{Process: previous.Process}, runtimeSecretSnapshot{Secrets: map[string]string{"DB": "latest"}}, newReady)
	sup.markStarted()
	sup.retireRuntimeSecretCommand(previous) // stale completion cannot retire a newer exec.
	if status, err := sup.deliverRuntimeSecretReload(syscall.SIGHUP, map[string]string{"DB": "newer"}, send); err != nil || status != "queued" || sent != 2 {
		t.Fatalf("old readiness leaked into restart: %s %v sent=%d", status, err, sent)
	}
	if status, err := sup.deliverRuntimeSecretReload(syscall.SIGHUP, map[string]string{"DB": "latest"}, send); err != nil || status != "not_attempted" {
		t.Fatalf("restart did not consume current values: %s %v", status, err)
	}
}

func TestRuntimeSecretReloadRetryBackoff(t *testing.T) {
	sup, ready := reloadTestSupervisor(t, map[string]string{"DB": "old"})
	if err := os.WriteFile(ready, []byte("ready\n"), 0600); err != nil {
		t.Fatal(err)
	}
	n := runtimeSecretNotification{revision: strings.Repeat("b", 64), values: map[string]string{"DB": "new"}}
	now := time.Now()
	send := func(*os.Process, syscall.Signal) error { return syscall.EPERM }
	for _, delay := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute} {
		status, attempted := n.attempt(now, sup, syscall.SIGHUP, send)
		if status != "failed" || !attempted || n.nextAttempt.Sub(now) != delay {
			t.Fatalf("backoff=%v, outcome=%s/%v", n.nextAttempt.Sub(now), status, attempted)
		}
		if _, attempted := n.attempt(now.Add(delay-time.Nanosecond), sup, syscall.SIGHUP, send); attempted {
			t.Fatal("retried before backoff")
		}
		now = now.Add(delay)
	}
	if status, attempted := n.attempt(now, sup, syscall.SIGHUP, func(*os.Process, syscall.Signal) error { return nil }); !attempted || status != "sent" {
		t.Fatalf("recovery=%s/%v", status, attempted)
	}
}

func TestRuntimeSecretReloadReadyMarker(t *testing.T) {
	dir := t.TempDir()
	p := &runtimeSecretProjection{secretsPath: filepath.Join(dir, "secrets.json"), uid: os.Getuid()}
	host, guest, err := prepareRuntimeSecretReadyFile(p, dir, true)
	if err != nil || guest != "/"+filepath.Base(host) {
		t.Fatalf("marker paths=%q/%q %v", host, guest, err)
	}
	for _, content := range []string{"", "ready", "ready\nextra", strings.Repeat("x", 64), "ready\n"} {
		if err := os.WriteFile(host, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		ready, err := runtimeSecretReady(host)
		if err != nil || ready != (content == "ready\n") {
			t.Fatalf("invalid marker accepted: ready=%v %v", ready, err)
		}
	}
	info, err := os.Stat(host)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("marker privacy: %v %v", info, err)
	}
	if _, _, err := prepareRuntimeSecretReadyFile(p, filepath.Join(dir, "other-root"), true); err == nil {
		t.Fatal("marker escaped its image")
	}
	if host, guest, err := prepareRuntimeSecretReadyFile(nil, "", false); err != nil || host != "" || guest != "" {
		t.Fatal("legacy image acquired a marker")
	}
}

func TestRuntimeSecretReloadWorkerRecovery(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("projection preparation requires root")
	}
	for _, scenario := range []string{"retry", "coalesce", "restart", "cancel", "publication_failure"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			state, err := newProjectedRuntimeSecretsState(map[string]string{"DB": "old"}, runtimeSecretProjection{secretsPath: filepath.Join(dir, "secrets.json"), revisionPath: filepath.Join(dir, "revision"), uid: 0})
			if err != nil {
				t.Fatal(err)
			}
			sup, ready := reloadTestSupervisor(t, map[string]string{"DB": "old"})
			if scenario == "retry" {
				if err := os.WriteFile(ready, []byte("ready\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			revision := strings.Repeat("b", 64)
			revisionAlias, err := os.Readlink(state.projection.revisionPath)
			if err != nil {
				t.Fatal(err)
			}
			fetches, sends := 0, 0
			reports := make(chan runtimeSecretReloadReport, 16)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				runRuntimeSecretReloadWorker(ctx, state, sup, slog.Default(), "worker", syscall.SIGHUP, runtimeSecretReloadIO{
					pollInterval: 20 * time.Millisecond, tickInterval: 5 * time.Millisecond,
					fetch: func(workload, previous string) (runtimeConfigResponse, error) {
						fetches++
						if workload != "worker" {
							panic("cross-workload fetch")
						}
						values := map[string]string{"DB": "new"}
						fetchRevision := revision
						if scenario == "coalesce" && fetches > 1 {
							fetchRevision = strings.Repeat("c", 64)
							values = map[string]string{}
						}
						if scenario == "publication_failure" && fetches > 1 {
							fetchRevision = strings.Repeat("c", 64)
							if fetches == 2 {
								if err := os.Remove(state.projection.revisionPath); err != nil {
									return runtimeConfigResponse{}, err
								}
								if err := os.Mkdir(state.projection.revisionPath, 0700); err != nil {
									return runtimeConfigResponse{}, err
								}
							}
							if fetches == 3 {
								if err := os.Remove(state.projection.revisionPath); err != nil {
									return runtimeConfigResponse{}, err
								}
								if err := os.Symlink(revisionAlias, state.projection.revisionPath); err != nil {
									return runtimeConfigResponse{}, err
								}
							}
						}
						if previous == fetchRevision {
							return runtimeConfigResponse{Revision: fetchRevision, Unchanged: true}, nil
						}
						return runtimeConfigResponse{Revision: fetchRevision, Secrets: &values}, nil
					},
					send: func(*os.Process, syscall.Signal) error {
						sends++
						if scenario == "retry" && sends == 1 {
							return syscall.EPERM
						}
						return nil
					},
					report: func(report runtimeSecretReloadReport) (bool, bool, error) { reports <- report; return true, false, nil },
				})
			}()
			t.Cleanup(func() { cancel(); <-done })
			first := waitReloadReport(t, reports)
			if first.Signal != map[bool]string{true: "failed", false: "queued"}[scenario == "retry"] {
				t.Fatalf("initial outcome=%+v", first)
			}
			if scenario == "cancel" {
				cancel()
				<-done
				if sends != 0 {
					t.Fatal("canceled worker delivered a signal")
				}
				return
			}
			if scenario == "coalesce" {
				second := waitReloadReport(t, reports)
				if second.Revision != strings.Repeat("c", 64) || second.Signal != "queued" {
					t.Fatalf("superseded outcome=%+v", second)
				}
			}
			if scenario == "publication_failure" {
				failed := waitReloadReport(t, reports)
				if failed.Projection != "failed" || failed.Signal != "not_attempted" {
					t.Fatalf("publication failure=%+v", failed)
				}
				resumed := waitReloadReport(t, reports)
				if resumed.Projection != "updated" || resumed.Signal != "queued" {
					t.Fatalf("lost notification after revision publication recovered: %+v", resumed)
				}
			}
			if scenario == "restart" {
				sup.trackRuntimeSecretCommand(&exec.Cmd{Process: sup.runtimeSecretStart.process}, state.startupSnapshot(), "")
				sup.markStarted()
			} else if scenario != "retry" {
				if err := os.WriteFile(ready, []byte("ready\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			final := waitReloadReport(t, reports)
			expectedRevision := revision
			if scenario == "coalesce" || scenario == "publication_failure" {
				expectedRevision = strings.Repeat("c", 64)
			}
			want := "sent"
			if scenario == "restart" {
				want = "not_attempted"
			}
			if final.Signal != want || final.ErrorCode != "" || final.WorkloadName != "worker" || final.Revision != expectedRevision {
				t.Fatalf("completion=%+v", final)
			}
			cancel()
			<-done
			if scenario == "retry" && (sends != 2 || fetches < 2) {
				t.Fatalf("unchanged revision did not retry: sends=%d fetches=%d", sends, fetches)
			}
			if scenario == "coalesce" && (sends != 1 || len(state.snapshot()) != 0) {
				t.Fatal("superseded notification or revocation was lost")
			}
		})
	}
}

func waitReloadReport(t *testing.T, reports <-chan runtimeSecretReloadReport) runtimeSecretReloadReport {
	t.Helper()
	select {
	case report := <-reports:
		return report
	case <-time.After(5 * time.Second):
		t.Fatal("reload observation not received")
		return runtimeSecretReloadReport{}
	}
}

func TestRuntimeSecretReloadSlowFetchDoesNotShortenBackoff(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("projection preparation requires root")
	}
	dir := t.TempDir()
	state, err := newProjectedRuntimeSecretsState(map[string]string{"DB": "old"}, runtimeSecretProjection{
		secretsPath: filepath.Join(dir, "secrets.json"), revisionPath: filepath.Join(dir, "revision"), uid: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	sup, ready := reloadTestSupervisor(t, map[string]string{"DB": "old"})
	if err := os.WriteFile(ready, []byte("ready\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	reports := make(chan runtimeSecretReloadReport, 2)
	var attempts []time.Time
	go func() {
		defer close(done)
		runRuntimeSecretReloadWorker(ctx, state, sup, slog.Default(), "", syscall.SIGHUP, runtimeSecretReloadIO{
			pollInterval: 10 * time.Millisecond, tickInterval: 5 * time.Millisecond,
			fetch: func(_, previous string) (runtimeConfigResponse, error) {
				revision := strings.Repeat("b", 64)
				if previous != "" {
					return runtimeConfigResponse{Revision: revision, Unchanged: true}, nil
				}
				time.Sleep(250 * time.Millisecond) // only the initial host fetch is slow.
				values := map[string]string{"DB": "new"}
				return runtimeConfigResponse{Revision: revision, Secrets: &values}, nil
			},
			send: func(*os.Process, syscall.Signal) error {
				attempts = append(attempts, time.Now())
				if len(attempts) == 1 {
					return syscall.EPERM
				}
				return nil
			},
			report: func(report runtimeSecretReloadReport) (bool, bool, error) { reports <- report; return true, false, nil },
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	if first := waitReloadReport(t, reports); first.Signal != "failed" {
		t.Fatalf("initial outcome=%+v", first)
	}
	if final := waitReloadReport(t, reports); final.Signal != "sent" {
		t.Fatalf("recovery=%+v", final)
	}
	cancel()
	<-done
	if len(attempts) != 2 || attempts[1].Sub(attempts[0]) < 950*time.Millisecond {
		t.Fatalf("a slow fetch shortened retry backoff: %v", attempts)
	}
}
