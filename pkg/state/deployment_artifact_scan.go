package state

// adr: 393

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/ociref"
)

// DeploymentArtifactScan is private immutable component scan evidence. It is
// neither a whole-runtime scan nor company approval or observed adoption.
type DeploymentArtifactScan struct {
	ID, InputHash        string
	Input                DeploymentArtifactScanInput
	ScannedAt, ExpiresAt time.Time
	Result               api.ScanResult
}
type DeploymentArtifactScanInput struct {
	ID               string          `json:"-"`
	RootfsProducerID string          `json:"rootfs_producer_id"`
	RootfsInputHash  string          `json:"rootfs_input_hash"`
	AccountID        string          `json:"account_id"`
	OrgID            string          `json:"org_id"`
	AppID            string          `json:"app_id"`
	DeploymentID     string          `json:"deployment_id"`
	WorkloadName     string          `json:"workload_name"`
	Scope            string          `json:"scope"`
	ImageReference   string          `json:"image_reference"`
	ArtifactDigest   string          `json:"artifact_digest"`
	ArtifactBytes    int64           `json:"artifact_bytes"`
	Status           string          `json:"status"`
	ScannerName      string          `json:"scanner_name,omitempty"`
	Report           *api.ScanResult `json:"report,omitempty"`
	Failure          string          `json:"failure,omitempty"`
}
type DeploymentArtifactScanStore interface {
	PublishDeploymentArtifactScan(context.Context, DeploymentArtifactScanInput) (DeploymentArtifactScan, error)
	// Historical retrieval does not assert current key/expiry/storage bytes.
	GetCurrentDeploymentArtifactScan(context.Context, string, string, string, string) (DeploymentArtifactScan, error)
}

func cloneArtifactScanResult(in api.ScanResult) api.ScanResult {
	if in.Vulnerabilities != nil {
		in.Vulnerabilities = append([]api.Vulnerability{}, in.Vulnerabilities...)
	}
	for i := range in.Vulnerabilities {
		in.Vulnerabilities[i].Paths = append([]string(nil), in.Vulnerabilities[i].Paths...)
	}
	return in
}
func cloneDeploymentArtifactScan(value DeploymentArtifactScan) DeploymentArtifactScan {
	if value.Input.Report != nil {
		report := cloneArtifactScanResult(*value.Input.Report)
		value.Input.Report = &report
	}
	value.Result = cloneArtifactScanResult(value.Result)
	return value
}
func prepareDeploymentArtifactScan(input DeploymentArtifactScanInput) (DeploymentArtifactScanInput, string, error) {
	in := cloneDeploymentArtifactScan(DeploymentArtifactScan{Input: input}).Input
	if !validStandardResourceRead(in.ID, in.RootfsProducerID) || !validStandardResourceRead(in.AccountID, in.AppID) || !validStandardResourceRead(in.DeploymentID, in.DeploymentID) || in.OrgID != "" && !validStandardResourceRead(in.OrgID, in.OrgID) || len(in.RootfsInputHash) != 64 || api.ValidateScope(in.Scope) != nil || in.ImageReference == "" || in.ArtifactBytes <= 0 || in.ArtifactBytes > api.ApplicationStandardBaseMaxArtifactBytes || ociref.ValidateDigest(in.ArtifactDigest) != nil || in.WorkloadName != "" && !api.ValidSidecarName(in.WorkloadName) {
		return in, "", ErrInvalidArgument
	}
	report, err := prepareProducerScanReport(in.Status, in.ScannerName, in.Failure, in.Report, in.ImageReference, in.ArtifactDigest)
	if err != nil {
		return in, "", err
	}
	in.Report = report
	in.ID, in.RootfsProducerID, in.AccountID, in.AppID, in.DeploymentID = canonicalStandardUUID(in.ID), canonicalStandardUUID(in.RootfsProducerID), canonicalStandardUUID(in.AccountID), canonicalStandardUUID(in.AppID), canonicalStandardUUID(in.DeploymentID)
	in.OrgID = registryCanonicalOrg(in.OrgID)
	hash, err := standardReviewDigest(in)
	return in, hash, err
}

func prepareProducerScanReport(status, scanner, failure string, report *api.ScanResult, image, digest string) (*api.ScanResult, error) {
	switch status {
	case "complete":
		if failure != "" || scanner != "grype" || report == nil {
			return nil, ErrInvalidArgument
		}
		value, err := prepareArtifactScanReport(cloneArtifactScanResult(*report), image, digest)
		if err != nil {
			return nil, err
		}
		return &value, nil
	case "failed":
		if report != nil || scanner != "" {
			return nil, ErrInvalidArgument
		}
		switch failure {
		case "artifact_read", "artifact_mismatch", "scanner_unavailable", "scanner_invalid":
			return nil, nil
		default:
			return nil, ErrInvalidArgument
		}
	default:
		return nil, ErrInvalidArgument
	}
}
func boundedScanMetadata(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= api.ApplicationStandardScanMaxMetadataBytes && !strings.ContainsAny(value, "\r\n\x00")
}
func prepareArtifactScanReport(report api.ScanResult, image, digest string) (api.ScanResult, error) {
	if report.Status != "" || report.ScannedAt != "" || report.Error != "" || report.ImageDigest != image || report.ArtifactDigest != digest || !boundedScanMetadata(report.ScannerVersion) || !boundedScanMetadata(report.ScannerDBVersion) || !strings.EqualFold(report.ScannerDBStatus, "valid") || report.Vulnerabilities == nil || len(report.Vulnerabilities) > api.ApplicationStandardScanMaxFindings {
		return report, ErrInvalidArgument
	}
	built, err := time.Parse(time.RFC3339Nano, report.ScannerDBBuiltAt)
	if err != nil {
		return report, ErrInvalidArgument
	}
	report.ScannerDBBuiltAt = built.UTC().Format(time.RFC3339Nano)
	report.ScannerDBStatus = "valid"
	counts := api.SeverityCounts{}
	for _, v := range report.Vulnerabilities {
		if len(v.Paths) > api.ApplicationStandardScanMaxPaths {
			return report, ErrInvalidArgument
		}
		for _, p := range v.Paths {
			if len(p) > api.ApplicationStandardScanMaxPathBytes || strings.ContainsAny(p, "\x00\r\n") {
				return report, ErrInvalidArgument
			}
		}
		switch v.Severity {
		case "CRITICAL":
			counts.Critical++
		case "HIGH":
			counts.High++
		case "MEDIUM":
			counts.Medium++
		case "LOW":
			counts.Low++
		case "UNKNOWN":
			counts.Unknown++
		default:
			return report, ErrInvalidArgument
		}
	}
	if counts != report.SeverityCounts {
		return report, ErrInvalidArgument
	}
	raw, err := json.Marshal(report)
	if err != nil || len(raw) > api.ApplicationStandardScanMaxReportBytes {
		return report, ErrInvalidArgument
	}
	return report, nil
}
func artifactScanResult(in DeploymentArtifactScanInput, at time.Time) api.ScanResult {
	report := api.ScanResult{ImageDigest: in.ImageReference, ArtifactDigest: in.ArtifactDigest, Vulnerabilities: []api.Vulnerability{}, Error: in.Failure}
	if in.Report != nil {
		report = cloneArtifactScanResult(*in.Report)
	}
	report.Status = in.Status
	report.ScannedAt = at.UTC().Format(time.RFC3339Nano)
	return report
}
func checkArtifactScanFreshness(in DeploymentArtifactScanInput, now time.Time) error {
	return checkProducerScanFreshness(in.Report, now)
}
func checkProducerScanFreshness(report *api.ScanResult, now time.Time) error {
	if report == nil {
		return nil
	}
	built, err := time.Parse(time.RFC3339Nano, report.ScannerDBBuiltAt)
	if err != nil || built.After(now) || now.Sub(built) > api.ApplicationStandardScannerDBMaxAge {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}
func checkArtifactScanParent(in DeploymentArtifactScanInput, root DeploymentRegistryRootfs, parent DeploymentRegistryVerification, dep Deployment, now time.Time) error {
	if err := validateRegistryRootfsStored(root); err != nil {
		return err
	}
	p := root.Input
	if p.RegistryVerificationID != parent.ID || p.RegistryInputHash != parent.InputHash || root.ExpiresAt.After(parent.ExpiresAt) || root.PublishedAt.Before(parent.VerifiedAt) {
		return ErrApplicationStandardRuntimeStale
	}
	if p.AccountID != parent.Input.AccountID || p.OrgID != parent.Input.OrgID || p.AppID != parent.Input.AppID || p.DeploymentID != parent.Input.DeploymentID || p.WorkloadName != parent.Input.WorkloadName {
		return ErrApplicationStandardRuntimeStale
	}
	if in.RootfsProducerID != root.ID || in.RootfsInputHash != root.InputHash || in.AccountID != p.AccountID || in.OrgID != p.OrgID || in.AppID != p.AppID || in.DeploymentID != p.DeploymentID || in.WorkloadName != p.WorkloadName || in.Scope != p.Scope || in.Scope != dep.Scope || in.ImageReference != parent.Input.ImageReference || in.ArtifactDigest != p.ArtifactDigest || in.ArtifactBytes != p.ArtifactBytes || !root.ExpiresAt.After(now) || !parent.ExpiresAt.After(now) {
		return ErrApplicationStandardRuntimeStale
	}
	if parent.Input.ImageChain == nil {
		return ErrApplicationStandardRuntimeStale
	}
	image, err := imagechain.Validate(parent.Input.ImageChain, parent.Input.Proof.SubjectDigest, parent.Input.SelectedDigest)
	if err != nil || imagechain.ValidateConsumption(image, p.LayerStart, p.Layers) != nil || p.Kind == "app-layer" && p.BaseProducerID == "" || p.Kind != "app-layer" && p.LayerStart != 0 {
		return ErrApplicationStandardRuntimeStale
	}
	switch dep.Status {
	case DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting, DeployLive, DeploySuperseded:
		return nil
	default:
		return ErrApplicationStandardRuntimeStale
	}
}
func validateDeploymentArtifactScan(value DeploymentArtifactScan) error {
	in, hash, err := prepareDeploymentArtifactScan(value.Input)
	if err != nil {
		return err
	}
	if in.ID != value.ID || hash != value.InputHash || value.ScannedAt.IsZero() || !value.ExpiresAt.After(value.ScannedAt) || value.ExpiresAt.Sub(value.ScannedAt) > api.ApplicationStandardArtifactScanTTL {
		return fmt.Errorf("artifact scan stored binding mismatch")
	}
	expected, err := standardReviewDigest(artifactScanResult(in, value.ScannedAt))
	if err != nil {
		return err
	}
	actual, err := standardReviewDigest(value.Result)
	if err != nil || actual != expected {
		return fmt.Errorf("artifact scan stored result mismatch")
	}
	return nil
}
