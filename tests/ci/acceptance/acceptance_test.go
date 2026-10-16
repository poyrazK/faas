package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func acceptanceEnv() map[string]string {
	return map[string]string{
		OptInEnv: "1", "GREGALE_CI_ACCEPTANCE_DATABASE_URL": "postgres://fixture:private-password@127.0.0.1:5432/ci_test",
		"GREGALE_CI_ACCEPTANCE_TLS_CERT": "/private/certificate.pem", "GREGALE_CI_ACCEPTANCE_TLS_KEY": "/private/tls-key.pem",
		"GREGALE_CI_ACCEPTANCE_ORIGIN": "https://ci-native.test.example:9443", "GREGALE_CI_ACCEPTANCE_LISTEN": "0.0.0.0:9443",
		"GREGALE_CI_ACCEPTANCE_GCS_BUCKET": "ci-native-private", "GREGALE_CI_ACCEPTANCE_IMAGE": "registry.test.example/ci@sha256:" + strings.Repeat("a", 64),
		"GREGALE_CI_ACCEPTANCE_SOURCE_SHA": strings.Repeat("b", 40), "GREGALE_CI_ACCEPTANCE_RUNNER_SHA256": strings.Repeat("c", 64),
		"GREGALE_CI_ACCEPTANCE_GUEST_SHA256": strings.Repeat("d", 64),
	}
}

func testConfig(t *testing.T) Config {
	t.Helper()
	env := acceptanceEnv()
	c, err := Load(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCIAcceptanceConfigurationFailsClosed(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{OptInEnv, ""}, {OptInEnv, "true"}, {"STORAGE_EMULATOR_HOST", "http://emulator"},
		{"GREGALE_CI_ACCEPTANCE_IMAGE", "registry.test.example/ci:latest"},
		{"GREGALE_CI_ACCEPTANCE_ORIGIN", "http://ci-native.test.example:9443"},
		{"GREGALE_CI_ACCEPTANCE_ORIGIN", "https://127.0.0.1:9443"},
		{"GREGALE_CI_ACCEPTANCE_ORIGIN", "https://10.0.0.1:9443"},
		{"GREGALE_CI_ACCEPTANCE_ORIGIN", "https://localhost:9443"},
		{"GREGALE_CI_ACCEPTANCE_ORIGIN", "https://ci-native.test.example:9443/?"},
		{"GREGALE_CI_ACCEPTANCE_LISTEN", "0.0.0.0:8443"},
		{"GREGALE_CI_ACCEPTANCE_SOURCE_SHA", "main"}, {"GREGALE_CI_ACCEPTANCE_RUNNER_SHA256", ""},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			env := acceptanceEnv()
			env[tc.key] = tc.value
			if _, err := Load(func(key string) string { return env[key] }); err == nil {
				t.Fatal("unqualified native configuration was accepted")
			}
		})
	}
	c := testConfig(t)
	data, err := json.Marshal(c)
	if err != nil || strings.Contains(string(data), "private-password") || strings.Contains(string(data), "tls-key.pem") {
		t.Fatal("configuration serialization exposed credentials or selectors")
	}
}

func completeJournal(t *testing.T) Journal {
	t.Helper()
	c := testConfig(t)
	id, err := DatabaseIdentity(c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	j := NewJournal(c, id)
	j.AccountID = uuid.NewString()
	j.Cleanup, j.NativeLeaks, j.SchemaGone, j.FilesGone = true, true, true, true
	j.Retired = []RetiredObject{{PhysicalKey: "ci-v1/" + j.AccountID + "/" + uuid.NewString(), Digest: "sha256:" + strings.Repeat("a", 64), Bytes: 4}}
	for _, name := range ScenarioNames() {
		s := Scenario{Status: "passed", ExecutionID: uuid.NewString(), MBSeconds: 5200, Checks: map[string]bool{}, Attempts: map[string]string{"unit": uuid.NewString()}, Instances: map[string]string{"unit": uuid.NewString()}}
		for _, check := range []string{"native_stop", "one_attempt", "image_snapshot", "expected_result", "http_admission", "http_status", "http_logs", "worker_progress", "two_jobs", "ordered_dependencies", "verified_download", "large_artifact", "billing", "apid_restarted", "schedd_restarted", "controller_restarted", "lost_ack", "concurrent_ci", "tenant_wake"} {
			s.Checks[check] = true
		}
		if name == "pipeline" {
			s.Attempts["package"], s.Instances["package"] = uuid.NewString(), uuid.NewString()
		}
		j.Scenarios[name] = s
	}
	return j
}

func TestCIAcceptanceVerdictRequiresEveryReceipt(t *testing.T) {
	good := completeJournal(t)
	if err := Verdict(good); err != nil {
		t.Fatal(err)
	}
	for _, name := range ScenarioNames() {
		for _, field := range []string{"scenario", "status", "native_stop", "one_attempt", "image_snapshot", "expected_result", "http_admission", "http_status", "http_logs", "worker_progress", "identity"} {
			t.Run(name+"/"+field, func(t *testing.T) {
				j := completeJournal(t)
				s := j.Scenarios[name]
				switch field {
				case "scenario":
					delete(j.Scenarios, name)
				case "status":
					s.Status = "skipped"
				case "identity":
					s.Instances["unit"] = ""
				default:
					s.Checks[field] = false
				}
				if field != "scenario" {
					j.Scenarios[name] = s
				}
				if err := Verdict(j); err == nil {
					t.Fatal("incomplete native evidence produced a passing verdict")
				}
			})
		}
	}
	for _, mutate := range []func(*Journal){
		func(j *Journal) { j.Cleanup = false }, func(j *Journal) { j.NativeLeaks = false },
		func(j *Journal) { j.SchemaGone = false }, func(j *Journal) { j.FilesGone = false }, func(j *Journal) { j.Retired = nil },
		func(j *Journal) { s := j.Scenarios["restart"]; s.Checks["lost_ack"] = false },
		func(j *Journal) { s := j.Scenarios["pipeline"]; s.Checks["large_artifact"] = false },
		func(j *Journal) { s := j.Scenarios["pipeline"]; s.MBSeconds = 0; j.Scenarios["pipeline"] = s },
		func(j *Journal) { s := j.Scenarios["wake_during_ci"]; s.Checks["tenant_wake"] = false },
	} {
		j := completeJournal(t)
		mutate(&j)
		if Verdict(j) == nil {
			t.Fatal("missing cleanup, provider or scenario proof passed")
		}
	}
}

func TestCIAcceptancePrivateJournalAndAuthority(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	j := completeJournal(t)
	if err := WriteJournal(dir, j); err != nil {
		t.Fatal(err)
	}
	read, err := ReadJournal(dir)
	if err != nil || read.RunID != j.RunID || Verdict(read) != nil {
		t.Fatal("durable journal did not retain its complete receipts")
	}
	before := authorityDigest(j)
	j.Bucket = "foreign-bucket"
	if authorityDigest(j) == before {
		t.Fatal("cleanup authority did not bind the exact bucket")
	}
	if err := os.Remove(filepath.Join(dir, "journal.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "journal.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadJournal(dir); err == nil {
		t.Fatal("journal followed a symlink")
	}
	c := testConfig(t)
	a, err := DatabaseIdentity(c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DatabaseIdentity(strings.Replace(c.DatabaseURL, "private-password", "rotated-password", 1))
	if err != nil || a != b {
		t.Fatal("credential rotation changed database identity")
	}
	b, err = DatabaseIdentity(c.DatabaseURL + "_foreign")
	if err != nil || a == b {
		t.Fatal("database identity did not isolate recovery")
	}
}

func TestCIAcceptanceSchemaOwnership(t *testing.T) {
	probe := pgtest.Open(t)
	probe.Close()
	dsn := os.Getenv("DATABASE_URL")
	id, err := DatabaseIdentity(dsn)
	if err != nil {
		t.Fatal(err)
	}
	j := NewJournal(testConfig(t), id)
	ctx := t.Context()
	pool, err := CreateSchema(ctx, dsn, j)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	t.Cleanup(func() {
		j.Cleanup, j.NativeLeaks = true, true // No workload or object is created by this ownership test.
		if err := DropSchema(context.Background(), dsn, j); err != nil {
			t.Error(err)
		}
	})
	foreign := j
	foreign.Bucket = "another-bucket"
	if opened, err := OpenSchema(ctx, dsn, foreign); err == nil {
		opened.Close()
		t.Fatal("altered bucket acquired schema cleanup authority")
	}
	if err := DropSchema(ctx, dsn, j); err == nil {
		t.Fatal("uncertified native stop allowed schema deletion")
	}
	opened, err := OpenSchema(ctx, dsn, j)
	if err != nil {
		t.Fatal(err)
	}
	opened.Close()
}
