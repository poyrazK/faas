package journeys

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// First-hour acceptance is opt-in even in the full tier. It holds one new app
// and submits four releases; it never changes an app that predates this run.
// This is a scripted CLI baseline, not evidence of interactive login or Start.
func TestFirstHourCLI(t *testing.T) {
	if os.Getenv("GREGALE_E2E_FIRST_HOUR") != "1" {
		t.Skip("opt-in: run scripts/first-hour-acceptance.sh")
	}
	tg := needs(t, tierFull, true)
	tg.acquireAppSlot(t)
	t.Cleanup(func() { <-tg.appSlots })
	slug := tg.slug("fh-" + randomHex(t, 6))
	initial := tg.run(t, time.Minute, "app", "get", slug)
	if initial.exit == 0 || initial.problemCode() != "not_found" {
		t.Fatalf("new test name must be absent before use: %s", initial.problem())
	}
	// Register before the first submission so a failed or lost receipt still
	// gets cleanup. No broad sweeper or inventory-based deletion is used.
	defer t.Run("cleanup", func(t *testing.T) {
		deleted := tg.run(t, 2*time.Minute, "app", "delete", slug, "--yes")
		if deleted.exit != 0 && deleted.problemCode() != "not_found" {
			t.Fatalf("could not delete this run's app %s: %s", slug, deleted.problem())
		}
		read := tg.run(t, time.Minute, "app", "get", slug)
		if read.exit == 0 || read.problemCode() != "not_found" {
			t.Fatalf("test app %s still resolves after cleanup", slug)
		}
	})
	s := newStory(t)
	source := probeSource(t, "first-hour-v1")
	started := time.Now()
	var appID, first, second, failed, repaired string
	var interruptedAt, recoveryStarted time.Time
	s.step("first_private_response", func(t *testing.T) {
		first, appID = firstHourSubmit(t, tg, slug, source)
		firstHourWait(t, tg, first, appID, true)
		firstHourVerify(t, tg, slug, first, appID, "first-hour-v1")
		app := object(t, tg.ok(t, time.Minute, "app", "get", slug).stdout)
		if field(app, "require_authn") != true {
			t.Fatal("verification changed or failed to retain private access")
		}
		t.Logf("metric first_hour_first_verified_response_s=%.3f", time.Since(started).Seconds())
	})
	s.check("anonymous_request_denied", func(t *testing.T) {
		r := get(tg.appURL(slug) + "/")
		if r.status != 401 && r.status != 403 {
			t.Fatalf("private app answered anonymously: HTTP %d", r.status)
		}
	})
	s.step("edit_and_interrupt_wait", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(source, "version.txt"), []byte("first-hour-v2\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var id string
		second, id = firstHourSubmit(t, tg, slug, source)
		if id != appID || second == first {
			t.Fatal("edit did not create a new release of the same app")
		}
		if err := interruptFirstHourWait(tg, second); err != nil {
			t.Fatal(err)
		}
		interruptedAt = time.Now()
		t.Log("metric first_hour_interruptions=1")
	})
	s.step("resume_same_release_and_verify_edit", func(t *testing.T) {
		firstHourWait(t, tg, second, appID, true)
		firstHourVerify(t, tg, slug, second, appID, "first-hour-v2")
		firstHourReleaseCount(t, tg, slug, 2)
		t.Logf("metric first_hour_resume_to_verified_response_s=%.3f", time.Since(interruptedAt).Seconds())
		t.Logf("metric first_hour_second_verified_deployment_s=%.3f", time.Since(started).Seconds())
	})
	s.step("failed_startup_is_diagnosable", func(t *testing.T) {
		original, err := os.ReadFile(filepath.Join(source, "server.js"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.WriteFile(filepath.Join(source, "server.js"), original, 0o600); err != nil {
				t.Error(err)
			}
		})
		broken := "console.error('FIRST_HOUR_STARTUP_FAILURE'); process.exit(1);\n" + string(original)
		if err := os.WriteFile(filepath.Join(source, "server.js"), []byte(broken), 0o600); err != nil {
			t.Fatal(err)
		}
		recoveryStarted = time.Now()
		var id string
		failed, id = firstHourSubmit(t, tg, slug, source)
		if id != appID {
			t.Fatal("failed release changed app identity")
		}
		firstHourWait(t, tg, failed, appID, false)
		logs := tg.ok(t, time.Minute, "logs", slug, "--deployment", failed)
		if !bytes.Contains(logs.stdout, []byte("FIRST_HOUR_STARTUP_FAILURE")) {
			t.Fatal("deployment logs do not expose the injected startup cause")
		}
	})
	s.step("repair_and_verify", func(t *testing.T) {
		// The preceding step's cleanup restores the source after diagnosis.
		if err := os.WriteFile(filepath.Join(source, "version.txt"), []byte("first-hour-v3\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var id string
		repaired, id = firstHourSubmit(t, tg, slug, source)
		if id != appID || repaired == failed {
			t.Fatal("repair did not create a new release of the same app")
		}
		firstHourWait(t, tg, repaired, appID, true)
		firstHourVerify(t, tg, slug, repaired, appID, "first-hour-v3")
		firstHourReleaseCount(t, tg, slug, 4)
		t.Logf("metric first_hour_failure_to_recovery_s=%.3f", time.Since(recoveryStarted).Seconds())
		t.Logf("metric first_hour_complete_s=%.3f", time.Since(started).Seconds())
		if time.Since(started) > time.Hour {
			t.Fatal("scripted first-hour journey exceeded one hour")
		}
	})
}

func firstHourSubmit(t *testing.T, tg *target, slug, source string) (string, string) {
	t.Helper()
	tg.deploySlots <- struct{}{}
	defer func() { <-tg.deploySlots }()
	result := tg.exec(t, tg.token, source, 3*time.Minute, "deploy", "--app", "--path", source,
		"--source=worktree", "--name", slug, "--require-authn", "--yes", "--no-wait", "--rollout", "immediate")
	if result.exit != 0 {
		t.Fatalf("submit failed; no automatic resubmission: %s", result.problem())
	}
	doc := object(t, result.stdout)
	id, appID := str(doc, "id"), str(doc, "app_id")
	if id == "" || appID == "" {
		t.Fatal("submission omitted the accepted deployment or app identity")
	}
	return id, appID
}

func firstHourWait(t *testing.T, tg *target, id, appID string, live bool) {
	t.Helper()
	result := tg.run(t, 11*time.Minute, "release", "wait", id, "--timeout", "10m")
	doc := object(t, result.stdout)
	if str(doc, "id") != id || str(doc, "app_id") != appID {
		t.Fatal("wait did not retain the accepted release/app identity")
	}
	if live && (result.exit != 0 || str(doc, "status") != "live") {
		t.Fatalf("release did not become live: %s", result.problem())
	}
	if !live && (result.exit == 0 || str(doc, "status") != "failed") {
		t.Fatalf("injected startup failure was not reported as failed: %s", result.problem())
	}
}

func firstHourVerify(t *testing.T, tg *target, slug, id, appID, version string) {
	t.Helper()
	result := tg.run(t, time.Minute, "release", "verify", id, "--app", slug, "--path", "/")
	if result.exit != 0 {
		t.Fatalf("owner verification failed: %s", result.problem())
	}
	if err := validateFirstHourEvidence(result.stdout, id, appID, version); err != nil {
		t.Fatal(err)
	}
}

func validateFirstHourEvidence(raw []byte, id, appID, version string) error {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("decode verification evidence: %w", err)
	}
	instance := strings.ReplaceAll(str(doc, "instance_id"), "-", "")
	_, instanceErr := hex.DecodeString(instance)
	if str(doc, "deployment_id") != id || str(doc, "app_id") != appID || str(doc, "scope") != "default" ||
		len(instance) != 32 || instanceErr != nil || strings.Trim(instance, "0") == "" || str(doc, "project_id") != "" || str(doc, "environment_id") != "" ||
		field(doc, "verified") != true || field(doc, "public_access_verified") != false || num(doc, "http_status") != 200 {
		return fmt.Errorf("verification did not prove the accepted private deployment and HTTP 200")
	}
	body := str(doc, "body")
	var response struct{ Version string }
	if len(body) > 4096 || json.Unmarshal([]byte(body), &response) != nil || response.Version != version {
		return fmt.Errorf("application did not serve the expected source version")
	}
	return nil
}

func firstHourReleaseCount(t *testing.T, tg *target, slug string, count int) {
	t.Helper()
	doc := object(t, tg.ok(t, time.Minute, "release", "list", "--app", slug).stdout)
	rows, _ := field(doc, "items").([]any)
	if len(rows) != count {
		t.Fatalf("expected %d submitted releases, found %d; resume must not redeploy", count, len(rows))
	}
}

type firstHourProgress struct {
	buffer bytes.Buffer
	once   sync.Once
	seen   chan struct{}
}

func (p *firstHourProgress) Write(b []byte) (int, error) {
	n, err := p.buffer.Write(b)
	if n > 0 {
		p.once.Do(func() { close(p.seen) })
	}
	return n, err
}

// Signal only after the waiter prints its first observed deployment state.
// A command that already completed is not counted as an interruption.
func interruptFirstHourWait(tg *target, id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, tg.cli, "--non-interactive", "release", "wait", id, "--progress", "--timeout", "50s")
	cmd.Env = append(tg.cliEnv(tg.token), "FAAS_JSON=0")
	cmd.Dir = tg.home
	progress := &firstHourProgress{seen: make(chan struct{})}
	cmd.Stdout = progress
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start deployment waiter: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("wait exited before interruption could be exercised: %w", err)
		}
		return errors.New("wait completed before interruption could be exercised")
	case <-ctx.Done():
		<-done
		return fmt.Errorf("waiter did not report progress before its deadline")
	case <-progress.seen:
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		<-done
		return fmt.Errorf("interrupt deployment waiter: %w", err)
	}
	if err := <-done; err == nil || ctx.Err() != nil {
		return fmt.Errorf("waiter was not interrupted before completion")
	}
	return nil
}
