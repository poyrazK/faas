package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promotionrollout"
)

func cmdPromotionRollout(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, "usage: gregalectl promotion-rollout <readiness|keygen|sign|pause|resume> [flags]")
		if len(args) != 0 {
			return 0
		}
		return 1
	}
	var err error
	var code int
	switch args[0] {
	case "readiness":
		code, err = promotionRolloutReadiness(args[1:])
	case "keygen":
		err = promotionRolloutKeygen(args[1:])
	case "sign":
		err = promotionRolloutSign(args[1:])
	case "pause", "resume":
		err = promotionRolloutPause(args[1:], args[0] == "pause")
	default:
		err = fmt.Errorf("unknown promotion-rollout command")
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "promotion-rollout: %v\n", err)
		if code != 0 {
			return code
		}
		return 1
	}
	return code
}

func rolloutCapabilities(value string) ([]promotionrollout.Capability, error) {
	var result []promotionrollout.Capability
	for _, name := range strings.Split(value, ",") {
		capability := promotionrollout.Capability(name)
		if !slices.Contains(promotionrollout.Capabilities(), capability) || slices.Contains(result, capability) {
			return nil, fmt.Errorf("invalid or repeated capability")
		}
		result = append(result, capability)
	}
	return result, nil
}

func promotionRolloutReadiness(args []string) (int, error) {
	fs := flag.NewFlagSet("promotion-rollout readiness", flag.ContinueOnError)
	policy := fs.String("policy", os.Getenv(promotionrollout.PolicyEnv), "absolute rollout policy path")
	trust := fs.String("trust-key", os.Getenv(promotionrollout.TrustKeyEnv), "trusted Ed25519 public key path")
	revision := fs.String("revision", "", "exact full revision of the APID binary being evaluated")
	account := fs.String("account-id", "", "target account UUID")
	project := fs.String("project-id", "", "target project UUID")
	enabled := fs.String("enabled-capabilities", "", "comma-separated APID boot flags; omitted uses the current environment")
	required := fs.String("require", "health", "comma-separated capabilities required for a zero exit")
	if err := fs.Parse(args); err != nil {
		return 1, err
	}
	if fs.NArg() != 0 || *revision == "" || uuid.Validate(*account) != nil || uuid.Validate(*project) != nil {
		return 1, fmt.Errorf("require revision, account-id and project-id")
	}
	capabilities, err := rolloutCapabilities(*required)
	if err != nil {
		return 1, err
	}
	overrides := map[string]string{promotionrollout.PolicyEnv: *policy, promotionrollout.TrustKeyEnv: *trust}
	if *enabled != "" {
		chosen, err := rolloutCapabilities(*enabled)
		if err != nil {
			return 1, err
		}
		for capability, name := range map[promotionrollout.Capability]string{promotionrollout.Health: "FAAS_PROMOTION_HEALTH_ENABLED", promotionrollout.ServiceGraph: "FAAS_PROMOTION_SERVICE_GRAPH_ENABLED", promotionrollout.Workers: "FAAS_PROMOTION_WORKERS_ENABLED", promotionrollout.Migrations: "FAAS_PROMOTION_MIGRATIONS_ENABLED"} {
			overrides[name] = "0"
			if slices.Contains(chosen, capability) {
				overrides[name] = "1"
			}
		}
	}
	gate := promotionrollout.FromEnv(func(name string) string {
		if value, ok := overrides[name]; ok {
			return value
		}
		return os.Getenv(name)
	}, *revision)
	target := promotionrollout.Target{AccountID: *account, ProjectID: *project}
	report := gate.Readiness(target)
	if jsonEnabled() {
		if err := json.NewEncoder(osStdout).Encode(report); err != nil {
			return 3, err
		}
	} else {
		if _, err := fmt.Fprintf(osStdout, "revision=%s mode=%s\n", report.SourceSHA, report.Mode); err != nil {
			return 3, err
		}
		for _, capability := range report.Capabilities {
			status := "blocked"
			if capability.Ready {
				status = "ready"
			}
			if _, err := fmt.Fprintf(osStdout, "%s: %s %s\n", capability.Capability, status, strings.Join(capability.Reasons, ", ")); err != nil {
				return 3, err
			}
		}
	}
	if !report.Allows(capabilities...) {
		return 3, nil
	}
	return 0, nil
}

func rolloutNewFile(path string, raw []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("output must be a new writable file")
	}
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("could not write output")
	}
	return nil
}

func promotionRolloutKeygen(args []string) error {
	fs := flag.NewFlagSet("promotion-rollout keygen", flag.ContinueOnError)
	private := fs.String("sign-key", "", "new private key file; keep off APID hosts")
	public := fs.String("trust-key", "", "new public key file for APID")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *private == "" || *public == "" || filepath.Clean(*private) == filepath.Clean(*public) {
		return fmt.Errorf("require distinct sign-key and trust-key paths")
	}
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		return fmt.Errorf("generate signing key")
	}
	if err := rolloutNewFile(*private, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0600); err != nil {
		return err
	}
	if err := rolloutNewFile(*public, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0644); err != nil {
		_ = os.Remove(*private)
		return err
	}
	return rolloutOutput("Signing key and public trust key created. Keep the signing key off APID hosts.", map[string]any{"operation": "keygen", "sign_key": *private, "trust_key": *public})
}

func promotionRolloutSign(args []string) error {
	fs := flag.NewFlagSet("promotion-rollout sign", flag.ContinueOnError)
	input := fs.String("input", "", "reviewed approval JSON including all supplemental gate receipts")
	keyPath := fs.String("sign-key", "", "private Ed25519 signing key")
	log := fs.String("native-log", "", "reviewed combined native log matching the report hash")
	coordinatedLog := fs.String("coordinated-log", "", "reviewed coordinated log; required for workers or coordinated capability")
	graphLog := fs.String("service-graph-log", "", "reviewed service-graph log; required for the service_graph capability")
	output := fs.String("output", "", "new signed approval file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *input == "" || *keyPath == "" || *log == "" || *output == "" {
		return fmt.Errorf("require input, sign-key, native-log and output")
	}
	raw, err := promotionrollout.ReadFile(*input, api.PromotionRolloutApprovalMaxBytes)
	if err != nil {
		return err
	}
	var approval promotionrollout.Approval
	if promotionrollout.Decode(raw, &approval) != nil || !promotionrollout.NativeLogMatches(approval.NativeReport, *log) {
		return fmt.Errorf("native report/log mismatch or invalid approval input")
	}
	if slices.Contains(approval.Capabilities, promotionrollout.ServiceGraph) && (*graphLog == "" || !promotionrollout.NativeLogMatches(approval.CapabilityReports[promotionrollout.ServiceGraph], *graphLog)) {
		return fmt.Errorf("service_graph requires its matching reviewed native log")
	}
	if (slices.Contains(approval.Capabilities, promotionrollout.Coordinated) || slices.Contains(approval.Capabilities, promotionrollout.Workers)) && (*coordinatedLog == "" || !promotionrollout.NativeLogMatches(approval.CapabilityReports[promotionrollout.Coordinated], *coordinatedLog)) {
		return fmt.Errorf("workers/coordinated requires its matching reviewed coordinated native log")
	}
	info, err := os.Lstat(*keyPath)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("signing key must be private")
	}
	raw, err = promotionrollout.ReadFile(*keyPath, 1024)
	if err != nil {
		return err
	}
	key, err := base64.StdEncoding.Strict().DecodeString(string(bytes.TrimSpace(raw)))
	if err != nil || len(key) != ed25519.PrivateKeySize || !bytes.Equal(ed25519.NewKeyFromSeed(key[:ed25519.SeedSize]), key) {
		return fmt.Errorf("invalid signing key")
	}
	signed, err := promotionrollout.Sign(approval, ed25519.PrivateKey(key), time.Now().UTC())
	if err != nil {
		return err
	}
	if err := rolloutNewFile(*output, signed, 0600); err != nil {
		return err
	}
	return rolloutOutput("Signed approval written. Admission remains controlled by policy, boot flags and the approved scope.", map[string]any{"operation": "sign", "output": *output, "source_sha": approval.SourceSHA})
}

func promotionRolloutPause(args []string, paused bool) error {
	fs := flag.NewFlagSet("promotion-rollout pause/resume", flag.ContinueOnError)
	path := fs.String("policy", os.Getenv(promotionrollout.PolicyEnv), "rollout policy path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *path == "" {
		return fmt.Errorf("require policy")
	}
	original, err := os.Lstat(*path)
	if err != nil {
		return promotionrollout.ErrInvalid
	}
	raw, err := promotionrollout.ReadFile(*path, api.PromotionRolloutPolicyMaxBytes)
	if err != nil {
		return err
	}
	var policy promotionrollout.Policy
	if promotionrollout.Decode(raw, &policy) != nil {
		return promotionrollout.ErrInvalid
	}
	policy.Paused = paused
	raw, err = json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(*path), ".promotion-policy-*")
	if err != nil {
		return fmt.Errorf("create policy replacement")
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err = preserveRolloutOwner(f, original); err == nil {
		err = f.Chmod(original.Mode().Perm())
	}
	if err == nil {
		_, err = f.Write(append(raw, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	current, statErr := os.Lstat(*path)
	if err != nil || statErr != nil || !os.SameFile(original, current) || original.Size() != current.Size() || !original.ModTime().Equal(current.ModTime()) || original.Mode() != current.Mode() || os.Rename(f.Name(), *path) != nil {
		return fmt.Errorf("replace policy atomically; policy may have changed")
	}
	return rolloutOutput(fmt.Sprintf("New promotion admissions paused=%t. Existing operations retain their recovery paths.", paused), map[string]any{"operation": "policy", "paused": paused})
}

func rolloutOutput(message string, fields map[string]any) error {
	if jsonEnabled() {
		return json.NewEncoder(osStdout).Encode(fields)
	}
	_, err := fmt.Fprintln(osStdout, message)
	return err
}
