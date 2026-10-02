package state

// adr: 431

import (
	"context"
	"encoding/json"
	"errors"
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
	ID               string `json:"-"`
	RootfsProducerID string `json:"rootfs_producer_id"`
	RootfsInputHash  string `json:"rootfs_input_hash"`
	// A separate current signature check may approve the same historical
	// conversion. Optional fields preserve pre-renewal immutable hashes.
	RegistryVerificationID string          `json:"registry_verification_id,omitempty"`
	RegistryInputHash      string          `json:"registry_input_hash,omitempty"`
	AccountID              string          `json:"account_id"`
	OrgID                  string          `json:"org_id"`
	AppID                  string          `json:"app_id"`
	DeploymentID           string          `json:"deployment_id"`
	WorkloadName           string          `json:"workload_name"`
	Scope                  string          `json:"scope"`
	ImageReference         string          `json:"image_reference"`
	ArtifactDigest         string          `json:"artifact_digest"`
	ArtifactBytes          int64           `json:"artifact_bytes"`
	Status                 string          `json:"status"`
	ScannerName            string          `json:"scanner_name,omitempty"`
	Report                 *api.ScanResult `json:"report,omitempty"`
	Failure                string          `json:"failure,omitempty"`
}
type DeploymentArtifactScanStore interface {
	PublishDeploymentArtifactScan(context.Context, DeploymentArtifactScanInput) (DeploymentArtifactScan, error)
	// Historical retrieval does not assert current key/expiry/storage bytes.
	GetCurrentDeploymentArtifactScan(context.Context, string, string, string, string) (DeploymentArtifactScan, error)
}

// These reads recheck current selections, publisher cryptography and leases.
// They do not read mutable artifact bytes, scan an overlaid runtime, or confer
// native admission/observed adoption. High-risk findings remain visible facts.
// A covered publication fence returns ErrApplicationStandardRuntimeBusy rather
// than waiting for its writer; evidence clocks are never renewed by a read.
type DeploymentArtifactScanEvidenceStore interface {
	GetFreshDeploymentArtifactScan(context.Context, string, string, string, string) (DeploymentArtifactScan, error)
	GetFreshDeploymentArtifactScanEvidence(context.Context, string, string, string) (DeploymentArtifactScanEvidence, error)
}

// Only an owned deployment with no retained producer lineage returns absent.
// Stale/missing selections in a deployment with lineage never become legacy.
var ErrDeploymentArtifactScanEvidenceAbsent = errors.New("state: deployment has no private artifact producer lineage")

type DeploymentArtifactScanEvidence struct {
	Components []DeploymentArtifactScan
	Bases      []BaseImageScan
	// Producer identities are captured under the same fences as these leases.
	// This is an input set, not native or whole-runtime approval.
	Artifacts            []DeploymentRuntimeArtifact
	CheckedAt, ExpiresAt time.Time
}

type artifactScanParents struct {
	Rootfs     DeploymentRegistryRootfs
	Approval   DeploymentRegistryVerification
	Deployment Deployment
}

func validArtifactScanEvidenceRead(accountID, appID, depID, workload string) bool {
	return validStandardResourceRead(accountID, appID) && validStandardResourceRead(depID, depID) && (workload == "" || api.ValidSidecarName(workload))
}

func checkDeploymentArtifactScanLease(value DeploymentArtifactScan, parents artifactScanParents, now time.Time) error {
	if err := validateDeploymentArtifactScan(value); err != nil {
		return err
	}
	limit := parents.Approval.ExpiresAt
	if value.Input.RegistryVerificationID == "" {
		limit = parents.Rootfs.ExpiresAt
	}
	if value.Input.Status != "complete" || value.ScannedAt.Before(parents.Rootfs.PublishedAt) || value.ScannedAt.Before(parents.Approval.VerifiedAt) || value.ScannedAt.After(now) || !value.ExpiresAt.After(now) || value.ExpiresAt.After(limit) {
		return ErrApplicationStandardRuntimeStale
	}
	return checkArtifactScanFreshness(value.Input, now)
}

func artifactScanWorkloads(raw []byte) ([]string, error) {
	var sidecars api.Sidecars
	if len(raw) != 0 && json.Unmarshal(raw, &sidecars) != nil || len(sidecars) > api.SidecarCapMax {
		return nil, ErrApplicationStandardRuntimeStale
	}
	names := []string{""}
	seen := map[string]bool{"": true}
	for _, sidecar := range sidecars {
		if !api.ValidSidecarName(sidecar.Name) || seen[sidecar.Name] {
			return nil, ErrApplicationStandardRuntimeStale
		}
		seen[sidecar.Name] = true
		if sidecar.Image != "" {
			names = append(names, sidecar.Name)
		}
	}
	return names, nil
}

func finishArtifactScanEvidence(value *DeploymentArtifactScanEvidence, now time.Time) error {
	value.CheckedAt = now
	for _, scan := range value.Components {
		if scan.ScannedAt.After(now) || !scan.ExpiresAt.After(now) || checkArtifactScanFreshness(scan.Input, now) != nil {
			return ErrApplicationStandardRuntimeStale
		}
		if value.ExpiresAt.IsZero() || scan.ExpiresAt.Before(value.ExpiresAt) {
			value.ExpiresAt = scan.ExpiresAt
		}
	}
	for _, scan := range value.Bases {
		if scan.ScannedAt.After(now) || !scan.ExpiresAt.After(now) || checkProducerScanFreshness(scan.Input.Report, now) != nil {
			return ErrApplicationStandardRuntimeStale
		}
		if value.ExpiresAt.IsZero() || scan.ExpiresAt.Before(value.ExpiresAt) {
			value.ExpiresAt = scan.ExpiresAt
		}
	}
	if len(value.Components) == 0 || !value.ExpiresAt.After(now) {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
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
	if in.RegistryVerificationID != "" {
		if !validStandardResourceRead(in.RegistryVerificationID, in.RegistryVerificationID) || len(in.RegistryInputHash) != 64 {
			return in, "", ErrInvalidArgument
		}
		in.RegistryVerificationID = canonicalStandardUUID(in.RegistryVerificationID)
	} else if in.RegistryInputHash != "" {
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
		case "artifact_read", "artifact_mismatch", "scanner_unavailable", "scanner_invalid", "publisher_unavailable", "publisher_invalid":
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
func checkArtifactScanParent(in DeploymentArtifactScanInput, root DeploymentRegistryRootfs, origin, parent DeploymentRegistryVerification, dep Deployment, now time.Time) error {
	if err := validateRegistryRootfsStored(root); err != nil {
		return err
	}
	p := root.Input
	if err := validateRegistryVerification(origin); err != nil {
		return err
	}
	if err := validateRegistryVerification(parent); err != nil {
		return err
	}
	if p.RegistryVerificationID != origin.ID || p.RegistryInputHash != origin.InputHash || root.ExpiresAt.After(origin.ExpiresAt) || root.PublishedAt.Before(origin.VerifiedAt) || root.PublishedAt.After(now) {
		return ErrApplicationStandardRuntimeStale
	}
	if p.AccountID != parent.Input.AccountID || p.OrgID != parent.Input.OrgID || p.AppID != parent.Input.AppID || p.DeploymentID != parent.Input.DeploymentID || p.WorkloadName != parent.Input.WorkloadName {
		return ErrApplicationStandardRuntimeStale
	}
	if in.RootfsProducerID != root.ID || in.RootfsInputHash != root.InputHash || in.AccountID != p.AccountID || in.OrgID != p.OrgID || in.AppID != p.AppID || in.DeploymentID != p.DeploymentID || in.WorkloadName != p.WorkloadName || in.Scope != p.Scope || in.Scope != dep.Scope || in.ImageReference != parent.Input.ImageReference || in.ArtifactDigest != p.ArtifactDigest || in.ArtifactBytes != p.ArtifactBytes || !parent.ExpiresAt.After(now) || parent.VerifiedAt.After(now) {
		return ErrApplicationStandardRuntimeStale
	}
	if in.RegistryVerificationID == "" {
		if parent.ID != origin.ID || !root.ExpiresAt.After(now) {
			return ErrApplicationStandardRuntimeStale
		}
	} else if in.RegistryVerificationID != parent.ID || in.RegistryInputHash != parent.InputHash || !sameRegistryScanSource(origin.Input, parent.Input) {
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

// Signature attachments/publishers can change, but the signed subject, child,
// config and layer chain of an existing conversion cannot change during renewal.
func sameRegistryScanSource(a, b DeploymentRegistryVerificationInput) bool {
	if a.AccountID != b.AccountID || a.OrgID != b.OrgID || a.AppID != b.AppID || a.DeploymentID != b.DeploymentID || a.WorkloadName != b.WorkloadName || a.ImageReference != b.ImageReference || a.SourceReference != b.SourceReference || a.SelectedReference != b.SelectedReference || a.SelectedDigest != b.SelectedDigest || a.Proof.SubjectDigest != b.Proof.SubjectDigest || a.ImageChain == nil || b.ImageChain == nil {
		return false
	}
	x, err := standardReviewDigest(a.ImageChain)
	y, otherErr := standardReviewDigest(b.ImageChain)
	return err == nil && otherErr == nil && x == y
}

func artifactScanApprovalID(in DeploymentArtifactScanInput, root DeploymentRegistryRootfs) string {
	if in.RegistryVerificationID != "" {
		return in.RegistryVerificationID
	}
	return root.Input.RegistryVerificationID
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
