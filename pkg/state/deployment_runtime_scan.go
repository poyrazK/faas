package state

// adr: 435. Immutable composed findings remain separate from company approval.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/runtimescan"
)

type DeploymentRuntimeScanReport struct {
	WorkloadName string         `json:"workload_name"`
	Report       api.ScanResult `json:"report"`
}

type DeploymentRuntimeScanInput struct {
	ID string `json:"-"`
	deploymentRuntimeArtifactIdentity
	Facts   runtimescan.Facts             `json:"facts"`
	Status  string                        `json:"status"`
	Reports []DeploymentRuntimeScanReport `json:"reports,omitempty"`
	Failure string                        `json:"failure,omitempty"`
}

// A stored scan does not approve findings or advance observed adoption. Its
// immutable lease cannot be renewed by retries, signature renewal or reads.
type DeploymentRuntimeScan struct {
	ID, InputHash        string
	Input                DeploymentRuntimeScanInput
	ScannedAt, ExpiresAt time.Time
}

type DeploymentRuntimeScanEvidence struct {
	Scan                 DeploymentRuntimeScan
	CheckedAt, ExpiresAt time.Time
}

type DeploymentRuntimeScanStore interface {
	PublishDeploymentRuntimeScan(context.Context, DeploymentRuntimeScanInput) (DeploymentRuntimeScan, error)
	// Selected history; this read makes no freshness or byte-consumption claim.
	GetCurrentDeploymentRuntimeScan(context.Context, string, string, string) (DeploymentRuntimeScan, error)
	GetFreshDeploymentRuntimeScan(context.Context, string, string, string) (DeploymentRuntimeScanEvidence, error)
}

func runtimeProducerSources(identity deploymentRuntimeArtifactIdentity) []runtimeadmission.ArtifactSource {
	sources := make([]runtimeadmission.ArtifactSource, 0, len(identity.Artifacts))
	for _, artifact := range identity.Artifacts {
		sources = append(sources, runtimeadmission.ArtifactSource{Kind: artifact.Kind, WorkloadName: artifact.WorkloadName,
			StorageKey: artifact.StorageKey, Digest: artifact.Digest, Bytes: artifact.Bytes})
	}
	return sources
}

func NewDeploymentRuntimeScanInput(id string, inputs DeploymentRuntimeProducerInputs, facts runtimescan.Facts, reports []DeploymentRuntimeScanReport) (DeploymentRuntimeScanInput, error) {
	in := DeploymentRuntimeScanInput{ID: id, deploymentRuntimeArtifactIdentity: inputs.deploymentRuntimeArtifactIdentity,
		Facts: facts, Status: "complete", Reports: reports}
	if facts.InputHash != inputs.InputHash {
		return DeploymentRuntimeScanInput{}, ErrInvalidArgument
	}
	in, _, err := prepareDeploymentRuntimeScan(in)
	return in, err
}

func NewFailedDeploymentRuntimeScanInput(id string, inputs DeploymentRuntimeProducerInputs, failure string) (DeploymentRuntimeScanInput, error) {
	hash, err := runtimeadmission.HashArtifactSources(runtimeProducerSources(inputs.deploymentRuntimeArtifactIdentity))
	if err != nil {
		return DeploymentRuntimeScanInput{}, err
	}
	in := DeploymentRuntimeScanInput{ID: id, deploymentRuntimeArtifactIdentity: inputs.deploymentRuntimeArtifactIdentity,
		Facts: runtimescan.Facts{Version: runtimescan.Version, InputHash: inputs.InputHash, SourcesHash: hash}, Status: "failed", Failure: failure}
	in, _, err = prepareDeploymentRuntimeScan(in)
	return in, err
}

func cloneDeploymentRuntimeScan(value DeploymentRuntimeScan) DeploymentRuntimeScan {
	value.Input.Artifacts = slices.Clone(value.Input.Artifacts)
	value.Input.Facts.Views = slices.Clone(value.Input.Facts.Views)
	value.Input.Reports = slices.Clone(value.Input.Reports)
	for i := range value.Input.Reports {
		value.Input.Reports[i].Report = cloneArtifactScanResult(value.Input.Reports[i].Report)
	}
	return value
}

func prepareDeploymentRuntimeScan(input DeploymentRuntimeScanInput) (DeploymentRuntimeScanInput, string, error) {
	in := cloneDeploymentRuntimeScan(DeploymentRuntimeScan{Input: input}).Input
	if !validStandardResourceRead(in.ID, in.ID) {
		return in, "", ErrInvalidArgument
	}
	identity, hash, err := prepareRuntimeArtifactIdentity(in.deploymentRuntimeArtifactIdentity)
	if err != nil {
		return in, "", ErrInvalidArgument
	}
	in.deploymentRuntimeArtifactIdentity, in.ID = identity, canonicalStandardUUID(in.ID)
	sources := runtimeProducerSources(identity)
	sourceHash, err := runtimeadmission.HashArtifactSources(sources)
	if err != nil || in.Facts.Version != runtimescan.Version || in.Facts.InputHash != hash || in.Facts.SourcesHash != sourceHash {
		return in, "", ErrInvalidArgument
	}
	if in.Status == "failed" {
		if len(in.Facts.Views) != 0 || len(in.Reports) != 0 {
			return in, "", ErrInvalidArgument
		}
		_, err = prepareProducerScanReport(in.Status, "", in.Failure, nil, "", "")
	} else if in.Status == "complete" && in.Failure == "" {
		err = prepareRuntimeScanReports(&in, sources)
	} else {
		err = ErrInvalidArgument
	}
	if err != nil {
		return in, "", err
	}
	slices.SortFunc(in.Facts.Views, func(a, b runtimescan.View) int { return strings.Compare(a.WorkloadName, b.WorkloadName) })
	slices.SortFunc(in.Reports, func(a, b DeploymentRuntimeScanReport) int { return strings.Compare(a.WorkloadName, b.WorkloadName) })
	digest, err := standardReviewDigest(in)
	return in, digest, err
}

func prepareRuntimeScanReports(in *DeploymentRuntimeScanInput, sources []runtimeadmission.ArtifactSource) error {
	if err := in.Facts.Check(in.Facts.InputHash, sources); err != nil {
		return ErrInvalidArgument
	}
	if len(in.Reports) != len(in.Facts.Views) {
		return ErrInvalidArgument
	}
	views := map[string]runtimescan.View{}
	for _, view := range in.Facts.Views {
		views[view.WorkloadName] = view
	}
	for i, report := range in.Reports {
		view, ok := views[report.WorkloadName]
		if !ok {
			return ErrInvalidArgument
		}
		value, err := prepareProducerScanReport("complete", "grype", "", &report.Report,
			"sha256:"+view.SourceTree.Digest, "sha256:"+in.Facts.SourcesHash)
		if err != nil {
			return err
		}
		// PostgreSQL stores microseconds. Round down before hashing so its
		// persisted DB-age deadline cannot exceed the canonical report's clock.
		built, _ := time.Parse(time.RFC3339Nano, value.ScannerDBBuiltAt)
		value.ScannerDBBuiltAt = built.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
		in.Reports[i].Report = *value
		delete(views, report.WorkloadName)
	}
	return nil
}

func runtimeScanDeadline(in DeploymentRuntimeScanInput, now, publisherExpiry time.Time) (time.Time, error) {
	expires := now.Add(api.ApplicationStandardArtifactScanTTL)
	if publisherExpiry.Before(expires) {
		expires = publisherExpiry
	}
	for _, view := range in.Reports {
		if err := checkProducerScanFreshness(&view.Report, now); err != nil {
			return time.Time{}, err
		}
		built, _ := time.Parse(time.RFC3339Nano, view.Report.ScannerDBBuiltAt)
		if limit := built.Add(api.ApplicationStandardScannerDBMaxAge); limit.Before(expires) {
			expires = limit
		}
	}
	if now.IsZero() || !expires.After(now) {
		return time.Time{}, ErrApplicationStandardRuntimeStale
	}
	return expires, nil
}

func validateDeploymentRuntimeScan(value DeploymentRuntimeScan) error {
	in, hash, err := prepareDeploymentRuntimeScan(value.Input)
	if err != nil {
		return err
	}
	limit, err := runtimeScanDeadline(in, value.ScannedAt, value.ExpiresAt)
	if err != nil || value.ID != in.ID || value.InputHash != hash || !value.ExpiresAt.After(value.ScannedAt) || value.ExpiresAt.After(limit) {
		return fmt.Errorf("runtime scan stored binding/clock mismatch")
	}
	return nil
}

func finishRuntimeScanEvidence(value DeploymentRuntimeScan, current DeploymentRuntimeProducerInputs) (DeploymentRuntimeScanEvidence, error) {
	// Callers validate the immutable record before taking the final storage clock.
	if value.Input.Status != "complete" || value.Input.Facts.InputHash != current.InputHash || value.ScannedAt.After(current.CheckedAt) || !value.ExpiresAt.After(current.CheckedAt) {
		return DeploymentRuntimeScanEvidence{}, ErrApplicationStandardRuntimeStale
	}
	expires, err := runtimeScanDeadline(value.Input, current.CheckedAt, current.ExpiresAt)
	if err != nil {
		return DeploymentRuntimeScanEvidence{}, err
	}
	if value.ExpiresAt.Before(expires) {
		expires = value.ExpiresAt
	}
	return DeploymentRuntimeScanEvidence{Scan: cloneDeploymentRuntimeScan(value), CheckedAt: current.CheckedAt, ExpiresAt: expires}, nil
}
