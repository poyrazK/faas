package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/fleetbundle"
	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/nodeclaim"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

const fleetEnrollmentWorkflowIdentity = `^https://github\.com/poyrazK/faas/\.github/workflows/fleet-enrollment\.yml@refs/heads/main$`

// fleetBundleVerifier is a seam for tests and for future private publisher
// deployments that may use a different cosign transport while keeping the
// bundle schema and join policy unchanged.
var fleetBundleVerifier = validateFleetEnrollmentBundleAt

type fleetBundleReport struct {
	File              string                 `json:"file"`
	SignatureFile     string                 `json:"signature_file"`
	Valid             bool                   `json:"valid"`
	Name              string                 `json:"name,omitempty"`
	Generation        uint64                 `json:"generation,omitempty"`
	IssuedAt          string                 `json:"issued_at,omitempty"`
	ExpiresAt         string                 `json:"expires_at,omitempty"`
	SignatureIdentity string                 `json:"signature_identity,omitempty"`
	ManifestChecked   bool                   `json:"manifest_checked,omitempty"`
	Claims            []fleetBundleClaimInfo `json:"claims,omitempty"`
	Errors            fleetbundle.Errors     `json:"errors,omitempty"`
	LoadError         string                 `json:"load_error,omitempty"`
}

type fleetBundleClaimInfo struct {
	Node          string `json:"node"`
	SSHHost       string `json:"ssh_host"`
	SSHUser       string `json:"ssh_user"`
	SSHPort       int    `json:"ssh_port"`
	HostKeySHA256 string `json:"host_key_sha256"`
}

func cmdDeployFleetBundle(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "gregalectl deploy fleet-bundle: missing subcommand; want create|validate")
		return 2
	}
	switch args[0] {
	case "create":
		return cmdDeployFleetBundleCreate(args[1:])
	case "validate":
		return cmdDeployFleetBundleValidate(args[1:])
	case flagHelpShort, flagHelpLong:
		fmt.Fprintln(os.Stderr, "usage: gregalectl deploy fleet-bundle create --claim-file PATH [--output PATH]")
		fmt.Fprintln(os.Stderr, "       gregalectl deploy fleet-bundle validate --file PATH --signature PATH [--manifest-file PATH]")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "gregalectl deploy fleet-bundle: unknown subcommand %q (expected: create|validate)\n", args[0])
		return 2
	}
}

func cmdDeployFleetBundleCreate(args []string) int {
	fs := flag.NewFlagSet("deploy fleet-bundle create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	claimPath := fs.String("claim-file", "", "provider-produced ComputeNodeClaim YAML/JSON (required)")
	manifestPath := fs.String("manifest-file", "", "optional production manifest to check the claim")
	name := fs.String("name", "production", "fleet authorization name")
	generation := fs.Uint64("generation", 0, "monotonically increasing authorization generation (required)")
	lifetime := fs.Duration("ttl", 24*time.Hour, "authorization lifetime, at most seven days")
	output := fs.String("output", "-", "output path; '-' writes YAML to stdout and never overwrites a file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *claimPath == "" || *generation == 0 {
		fmt.Fprintln(os.Stderr, "gregalectl deploy fleet-bundle create: --claim-file and a non-zero --generation are required")
		return 2
	}
	claim, err := nodeclaim.Load(*claimPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy fleet-bundle create: %v\n", err)
		return 1
	}
	if errs := claim.Validate(); errs != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy fleet-bundle create: invalid claim: %v\n", errs)
		return 1
	}
	bundle, err := fleetbundle.New(*name, *generation, time.Now().UTC(), *lifetime, []nodeclaim.Claim{*claim})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy fleet-bundle create: %v\n", err)
		return 1
	}
	if *manifestPath != "" {
		if errs, err := validateFleetBundleManifest(&bundle, *manifestPath); err != nil {
			fmt.Fprintf(os.Stderr, "gregalectl deploy fleet-bundle create: %v\n", err)
			return 1
		} else if errs != nil {
			fmt.Fprintf(os.Stderr, "gregalectl deploy fleet-bundle create: claim is not authorized by manifest: %v\n", errs)
			return 1
		}
	}
	body, err := fleetbundle.Marshal(bundle)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy fleet-bundle create: encode bundle: %v\n", err)
		return 1
	}
	if *output == "-" {
		if _, err := os.Stdout.Write(body); err != nil {
			fmt.Fprintf(os.Stderr, "gregalectl deploy fleet-bundle create: write stdout: %v\n", err)
			return 3
		}
	} else if err := writeNewFleetBundle(*output, body); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy fleet-bundle create: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "created unsigned FleetEnrollmentBundle name=%s generation=%d digest=%s; sign these exact bytes before use\n", bundle.Metadata.Name, bundle.Metadata.Generation, fleetbundle.Digest(body))
	return 0
}

func writeNewFleetBundle(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create %s without overwriting an existing file: %w", path, err)
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

func cmdDeployFleetBundleValidate(args []string) int {
	fs := flag.NewFlagSet("deploy fleet-bundle validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	filePath := fs.String("file", "", "signed FleetEnrollmentBundle YAML/JSON file (required)")
	signaturePath := fs.String("signature", "", "detached cosign signature bundle (required)")
	manifestPath := fs.String("manifest-file", "", "optional signed production manifest to check all claims")
	cosignPath := fs.String("cosign-binary", "cosign", "cosign verifier binary")
	jsonOut := fs.Bool("json", false, "emit JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *filePath == "" || *signaturePath == "" {
		fmt.Fprintln(os.Stderr, "gregalectl deploy fleet-bundle validate: --file and --signature are required")
		return 2
	}

	report := fleetBundleReport{File: *filePath, SignatureFile: *signaturePath}
	bundle, identity, errs, err := fleetBundleVerifier(
		*filePath, *signaturePath, *manifestPath, *cosignPath, time.Now().UTC(),
	)
	if err != nil {
		report.LoadError = err.Error()
		return emitFleetBundleReport(report, *jsonOut || jsonOutput, 3)
	}
	report.Valid = len(errs) == 0
	report.Errors = errs
	report.Name = bundle.Metadata.Name
	report.Generation = bundle.Metadata.Generation
	report.IssuedAt = bundle.Spec.IssuedAt.UTC().Format(time.RFC3339)
	report.ExpiresAt = bundle.Spec.ExpiresAt.UTC().Format(time.RFC3339)
	report.SignatureIdentity = identity
	report.ManifestChecked = *manifestPath != ""
	for _, claim := range bundle.Spec.Claims {
		n := claim.Normalize()
		report.Claims = append(report.Claims, fleetBundleClaimInfo{
			Node: n.Name, SSHHost: n.SSHHost, SSHUser: n.SSHUser, SSHPort: n.SSHPort,
			HostKeySHA256: n.HostKeySHA256,
		})
	}
	if report.Valid {
		return emitFleetBundleReport(report, *jsonOut || jsonOutput, 0)
	}
	return emitFleetBundleReport(report, *jsonOut || jsonOutput, 1)
}

func emitFleetBundleReport(report fleetBundleReport, jsonOut bool, code int) int {
	if jsonOut {
		jsonEmit(os.Stdout, report)
		return code
	}
	if report.LoadError != "" {
		fmt.Fprintf(os.Stderr, "gregalectl deploy fleet-bundle validate: %s\n", report.LoadError)
		return code
	}
	if report.Valid {
		_, _ = fmt.Fprintf(os.Stdout, "valid FleetEnrollmentBundle name=%s generation=%d claims=%d expires=%s\n", report.Name, report.Generation, len(report.Claims), report.ExpiresAt)
		return code
	}
	for _, validationErr := range report.Errors {
		fmt.Fprintf(os.Stderr, "gregalectl deploy fleet-bundle validate: %s\n", validationErr)
	}
	return code
}

func validateFleetEnrollmentBundleAt(bundlePath, signaturePath, manifestPath, cosignPath string, now time.Time) (*fleetbundle.Bundle, string, fleetbundle.Errors, error) {
	if bundlePath == "" || signaturePath == "" {
		return nil, "", nil, fmt.Errorf("fleet-bundle: bundle and detached signature paths are required")
	}
	identityRegexp := regexp.MustCompile(fleetEnrollmentWorkflowIdentity)
	verifier := releaseinstall.NewExecCosignVerifier(releaseinstall.CosignVerifyConfig{
		Issuer:         "https://token.actions.githubusercontent.com",
		IdentityRegexp: identityRegexp,
		CosignPath:     cosignPath,
	})
	identity, err := verifier.VerifyBlob(context.Background(), bundlePath, signaturePath)
	if err != nil {
		return nil, "", nil, fmt.Errorf("fleet-bundle: signature verification failed: %w", err)
	}
	bundle, err := fleetbundle.Load(bundlePath)
	if err != nil {
		return nil, "", nil, err
	}
	errs := bundle.ValidateAt(now)
	if manifestPath != "" && len(errs) == 0 {
		manifestErrs, checkErr := validateFleetBundleManifest(bundle, manifestPath)
		if checkErr != nil {
			return nil, "", nil, checkErr
		}
		errs = append(errs, manifestErrs...)
	}
	return bundle, identity, errs, nil
}

func validateFleetBundleManifest(bundle *fleetbundle.Bundle, path string) (fleetbundle.Errors, error) {
	m, err := manifest.Load(path)
	if err != nil {
		return nil, fmt.Errorf("fleet-bundle: read production manifest: %w", err)
	}
	if errs := m.Validate(); errs != nil {
		return nil, fmt.Errorf("fleet-bundle: invalid production manifest: %w", errs)
	}
	manifestHosts := make(map[string]string, len(m.Fleet.Hosts))
	for _, host := range m.Fleet.Hosts {
		manifestHosts[host.Name] = host.Role
	}
	var errs fleetbundle.Errors
	dynamicClaims := 0
	for i, claim := range bundle.Spec.Claims {
		role, ok := manifestHosts[claim.Metadata.Name]
		path := fmt.Sprintf("spec.claims[%d].metadata.name", i)
		if !ok {
			if _, dynamicErr := m.DynamicComputeHost(claim.Metadata.Name, claim.Spec.Storage.Device); dynamicErr != nil {
				errs = append(errs, fleetbundle.Error{Path: path, Message: fmt.Sprintf("production manifest does not authorize dynamic node %q: %v", claim.Metadata.Name, dynamicErr)})
				continue
			}
			dynamicClaims++
			if m.Fleet.ComputeNodeCount()+dynamicClaims > m.Fleet.DynamicCompute.MaxNodes {
				errs = append(errs, fleetbundle.Error{Path: path, Message: fmt.Sprintf("dynamic compute policy allows at most %d compute nodes", m.Fleet.DynamicCompute.MaxNodes)})
			}
			continue
		}
		if role != roleComputeOnly {
			errs = append(errs, fleetbundle.Error{Path: path, Message: fmt.Sprintf("production manifest node %q has role %q; enrollment requires compute-only", claim.Metadata.Name, role)})
		}
	}
	return errs, nil
}

// resolveFleetBundleInputs verifies the detached signature and authorization
// window before a join can contact the provider host. It also copies the one
// claim selected by --node into the existing join options and rejects any
// conflicting legacy flags.
func resolveFleetBundleInputs(opts *deployJoinOptions) error {
	if opts.FleetBundleFile == "" && opts.FleetBundleSignature == "" {
		return nil
	}
	if opts.FleetBundleFile == "" || opts.FleetBundleSignature == "" {
		return fmt.Errorf("fleet-bundle: --fleet-bundle-file and --fleet-bundle-signature must be supplied together")
	}
	cosignPath := opts.CosignBinary
	if cosignPath == "" {
		cosignPath = "cosign"
	}
	bundle, _, errs, err := fleetBundleVerifier(
		opts.FleetBundleFile, opts.FleetBundleSignature, opts.ManifestFile, cosignPath, time.Now().UTC(),
	)
	if err != nil {
		return err
	}
	if errs != nil {
		return fmt.Errorf("fleet-bundle: authorization rejected: %w", errs)
	}
	if opts.Node == "" {
		if len(bundle.Spec.Claims) != 1 {
			return fmt.Errorf("fleet-bundle: --node is required when the bundle contains %d claims", len(bundle.Spec.Claims))
		}
		opts.Node = bundle.Spec.Claims[0].Metadata.Name
	}
	claim, err := bundle.ClaimForNode(opts.Node)
	if err != nil {
		return err
	}
	if opts.SSHHost != "" && opts.SSHHost != claim.SSHHost {
		return fmt.Errorf("fleet-bundle: --ssh-host %q conflicts with the signed claim %q", opts.SSHHost, claim.SSHHost)
	}
	if opts.SSHUser != "" && opts.SSHUser != claim.SSHUser {
		return fmt.Errorf("fleet-bundle: --ssh-user %q conflicts with the signed claim %q", opts.SSHUser, claim.SSHUser)
	}
	if opts.SSHPort != 0 && opts.SSHPort != claim.SSHPort {
		return fmt.Errorf("fleet-bundle: --ssh-port %d conflicts with the signed claim %d", opts.SSHPort, claim.SSHPort)
	}
	if opts.SSHHostKeySHA256 != "" && opts.SSHHostKeySHA256 != claim.HostKeySHA256 {
		return fmt.Errorf("fleet-bundle: --ssh-host-key-sha256 conflicts with the signed claim")
	}
	if opts.StorageDevice != "" && opts.StorageDevice != claim.StorageDevice {
		return fmt.Errorf("fleet-bundle: --storage-device conflicts with the signed claim")
	}
	if opts.FormatStorage && !claim.FormatStorage {
		return fmt.Errorf("fleet-bundle: --format-storage conflicts with the signed claim")
	}
	opts.SSHHost = claim.SSHHost
	opts.SSHUser = claim.SSHUser
	opts.SSHPort = claim.SSHPort
	opts.SSHHostKeySHA256 = claim.HostKeySHA256
	opts.StorageDevice = claim.StorageDevice
	opts.FormatStorage = claim.FormatStorage
	if !opts.DryRun && opts.FleetReplayState == "" {
		return fleetbundle.ErrStateRequired
	}
	if opts.FleetReplayState != "" {
		if !filepath.IsAbs(opts.FleetReplayState) {
			return fmt.Errorf("fleet-bundle: --fleet-replay-state must be an absolute durable path")
		}
		if err := (fleetbundle.ReplayState{Dir: opts.FleetReplayState}).Check(*bundle, opts.Node); err != nil {
			return err
		}
	}
	opts.FleetBundleVerified = true
	return nil
}

func markFleetBundleConsumed(opts deployJoinOptions) error {
	if opts.FleetBundleFile == "" {
		return nil
	}
	bundle, err := fleetbundle.Load(opts.FleetBundleFile)
	if err != nil {
		return err
	}
	if err := (fleetbundle.ReplayState{Dir: opts.FleetReplayState}).Mark(*bundle, opts.Node); err != nil {
		return fmt.Errorf("fleet-bundle: record consumption: %w", err)
	}
	return nil
}
