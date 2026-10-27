package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/internal/promotionrollouttest"
	"github.com/onebox-faas/faas/pkg/promotionrollout"
)

func TestPromotionRolloutReadinessAndPause(t *testing.T) {
	target := promotionrollout.Target{AccountID: uuid.NewString(), ProjectID: uuid.NewString()}
	f := promotionrollouttest.New(t, target)
	var output bytes.Buffer
	previous, previousJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = previous, previousJSON })
	t.Setenv(promotionrollout.QualificationEnv, "0")
	args := []string{"readiness", "--policy", f.PolicyPath, "--trust-key", f.PublicPath, "--revision", promotionrollouttest.Revision, "--account-id", target.AccountID, "--project-id", target.ProjectID, "--enabled-capabilities", "health,migrations", "--require", "migrations"}
	if code := cmdPromotionRollout(args); code != 0 {
		t.Fatalf("readiness=%d %s", code, output.String())
	}
	var report promotionrollout.Readiness
	if err := json.Unmarshal(output.Bytes(), &report); err != nil || !report.Capabilities[0].Ready || report.Capabilities[1].Ready {
		t.Fatal("incorrect JSON readiness", err)
	}
	if err := os.Chmod(f.PolicyPath, 0640); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"pause", "resume"} {
		output.Reset()
		if code := cmdPromotionRollout([]string{operation, "--policy", f.PolicyPath}); code != 0 {
			t.Fatal(operation, code)
		}
		var result map[string]any
		if json.Unmarshal(output.Bytes(), &result) != nil || result["paused"] != (operation == "pause") {
			t.Fatal("mutation output is not JSON")
		}
		info, err := os.Stat(f.PolicyPath)
		if err != nil || info.Mode().Perm() != 0640 {
			t.Fatal("lost policy permissions", err)
		}
		output.Reset()
		code := cmdPromotionRollout(args)
		if (code == 0) != (operation == "resume") {
			t.Fatal("policy admission not updated", code, output.String())
		}
	}
	output.Reset()
	args = append(args, "--project-id", uuid.NewString())
	if code := cmdPromotionRollout(args); code != 3 {
		t.Fatal("outside cohort readiness did not fail", code)
	}
}

func TestPromotionRolloutSigningAndKeyIsolation(t *testing.T) {
	target := promotionrollout.Target{AccountID: uuid.NewString(), ProjectID: uuid.NewString()}
	f := promotionrollouttest.New(t, target)
	var output bytes.Buffer
	previous := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = previous })
	directory := t.TempDir()
	private, public := filepath.Join(directory, "sign.key"), filepath.Join(directory, "trust.pub")
	keygen := []string{"keygen", "--sign-key", private, "--trust-key", public}
	if code := cmdPromotionRollout(keygen); code != 0 {
		t.Fatal("keygen", code)
	}
	keyBytes, err := os.ReadFile(private)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(private)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private key exposure")
	}
	if strings.Contains(output.String(), strings.TrimSpace(string(keyBytes))) {
		t.Fatal("printed signing key")
	}
	if code := cmdPromotionRollout(keygen); code == 0 {
		t.Fatal("overwrote existing keys")
	}
	log := []byte("synthetic unit-test log, never native qualification\n")
	logPath := filepath.Join(directory, "native.log")
	if err := os.WriteFile(logPath, log, 0600); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	_ = json.Unmarshal(f.Approval.NativeReport, &report)
	hash := sha256.Sum256(log)
	report["log_sha256"] = hex.EncodeToString(hash[:])
	f.Approval.NativeReport, _ = json.Marshal(report)
	f.Approval.Capabilities = []promotionrollout.Capability{promotionrollout.Health}
	f.Approval.CapabilityReports = nil
	input := filepath.Join(directory, "input.json")
	raw, _ := json.Marshal(f.Approval)
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "approved.json")
	args := []string{"sign", "--input", input, "--sign-key", private, "--native-log", logPath, "--output", path}
	if code := cmdPromotionRollout(args); code != 0 {
		t.Fatal("sign", code)
	}
	f.Policy.ApprovalPath = path
	f.WritePolicy(t)
	publicRaw, err := os.ReadFile(public)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := promotionrollout.PublicKey(publicRaw)
	if err != nil {
		t.Fatal(err)
	}
	gate := promotionrollout.New(promotionrollout.Config{PolicyPath: f.PolicyPath, PublicKey: trust, SourceSHA: promotionrollouttest.Revision, Enabled: map[promotionrollout.Capability]bool{promotionrollout.Health: true}})
	if !gate.Check(target, promotionrollout.Health) {
		t.Fatal("signed approval rejected")
	}
	if code := cmdPromotionRollout(args); code == 0 {
		t.Fatal("overwrote approval without an explicit new output")
	}
	if err := os.WriteFile(logPath, []byte("different log"), 0600); err != nil {
		t.Fatal(err)
	}
	args[len(args)-1] = filepath.Join(directory, "bad.json")
	if code := cmdPromotionRollout(args); code == 0 {
		t.Fatal("signed mismatched log/report")
	}
	if _, err := os.Stat(args[len(args)-1]); !os.IsNotExist(err) {
		t.Fatal("failed signing left approval")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyBytes)))
	if err != nil || len(decoded) != 64 {
		t.Fatal("invalid generated key")
	}
}

func TestPromotionRolloutSigningRequiresMatchingServiceGraphLog(t *testing.T) {
	f := promotionrollouttest.New(t, promotionrollout.Target{AccountID: uuid.NewString(), ProjectID: uuid.NewString()})
	f.Approval.Capabilities = []promotionrollout.Capability{promotionrollout.Health, promotionrollout.ServiceGraph}
	directory := t.TempDir()
	basePath, graphPath, keyPath, inputPath := filepath.Join(directory, "base.log"), filepath.Join(directory, "graph.log"), filepath.Join(directory, "sign.key"), filepath.Join(directory, "input.json")
	for path, raw := range map[string][]byte{basePath: []byte("synthetic base unit log"), graphPath: []byte("synthetic graph unit log"), keyPath: []byte(base64.StdEncoding.EncodeToString(f.Private))} {
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	bind := func(raw json.RawMessage, path string) json.RawMessage {
		var report map[string]any
		if err := json.Unmarshal(raw, &report); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		report["log_sha256"] = hex.EncodeToString(hash[:])
		result, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	f.Approval.NativeReport = bind(f.Approval.NativeReport, basePath)
	f.Approval.CapabilityReports[promotionrollout.ServiceGraph] = bind(f.Approval.CapabilityReports[promotionrollout.ServiceGraph], graphPath)
	data, err := os.ReadFile(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	for _, name := range []string{"service-graph-routing", "service-graph-recovery", "service-graph-isolation"} {
		receipt := f.Approval.Gates[name]
		receipt.EvidenceSHA256 = hex.EncodeToString(hash[:])
		f.Approval.Gates[name] = receipt
	}
	raw, err := json.Marshal(f.Approval)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", basePath, graphPath} {
		output := filepath.Join(directory, "output.json")
		args := []string{"--input", inputPath, "--sign-key", keyPath, "--native-log", basePath, "--output", output}
		if path != "" {
			args = append(args, "--service-graph-log", path)
		}
		err := promotionRolloutSign(args)
		if (err == nil) != (path == graphPath) {
			t.Fatal("service graph log binding", path, err)
		}
		if path != graphPath {
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("failed signing left output")
			}
		}
	}
}

func TestPromotionRolloutSigningRequiresCoordinatedLog(t *testing.T) {
	f := promotionrollouttest.New(t, promotionrollout.Target{AccountID: uuid.NewString(), ProjectID: uuid.NewString()})
	f.Approval.Capabilities = []promotionrollout.Capability{promotionrollout.Health, promotionrollout.Workers, promotionrollout.Migrations, promotionrollout.Coordinated}
	directory := t.TempDir()
	basePath, coordinatedPath, keyPath, inputPath := filepath.Join(directory, "base.log"), filepath.Join(directory, "coordinated.log"), filepath.Join(directory, "sign.key"), filepath.Join(directory, "input.json")
	for path, raw := range map[string][]byte{basePath: []byte("synthetic base unit log"), coordinatedPath: []byte("synthetic coordinated unit log"), keyPath: []byte(base64.StdEncoding.EncodeToString(f.Private))} {
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	bind := func(raw json.RawMessage, path string) json.RawMessage {
		var report map[string]any
		if err := json.Unmarshal(raw, &report); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		report["log_sha256"] = hex.EncodeToString(hash[:])
		result, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	f.Approval.NativeReport = bind(f.Approval.NativeReport, basePath)
	f.Approval.CapabilityReports[promotionrollout.Coordinated] = bind(f.Approval.CapabilityReports[promotionrollout.Coordinated], coordinatedPath)
	data, err := os.ReadFile(coordinatedPath)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	for _, name := range []string{"coordinated-migration-workers", "worker-replacement-resolve"} {
		receipt := f.Approval.Gates[name]
		receipt.EvidenceSHA256 = hex.EncodeToString(hash[:])
		f.Approval.Gates[name] = receipt
	}
	raw, err := json.Marshal(f.Approval)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(inputPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", basePath, coordinatedPath} {
		output := filepath.Join(directory, "output.json")
		args := []string{"--input", inputPath, "--sign-key", keyPath, "--native-log", basePath, "--output", output}
		if path != "" {
			args = append(args, "--coordinated-log", path)
		}
		err := promotionRolloutSign(args)
		if (err == nil) != (path == coordinatedPath) {
			t.Fatal("coordinated log binding", path, err)
		}
		if path != coordinatedPath {
			if _, err = os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("failed signing left output")
			}
		}
	}
}
