package state

// adr: 431

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
)

// BaseImageScan is private scan evidence for the complete shared drive. It
// confers neither company approval nor native admission authority.
type BaseImageScan struct {
	ID, InputHash        string
	Input                BaseImageScanInput
	ScannedAt, ExpiresAt time.Time
	Result               api.ScanResult
}
type BaseImageScanInput struct {
	ID              string                  `json:"-"`
	BaseProducerID  string                  `json:"base_producer_id"`
	BaseInputHash   string                  `json:"base_input_hash"`
	Artifact        imagechain.BaseArtifact `json:"artifact"`
	SourceReference string                  `json:"source_reference"`
	Status          string                  `json:"status"`
	ScannerName     string                  `json:"scanner_name,omitempty"`
	Report          *api.ScanResult         `json:"report,omitempty"`
	Failure         string                  `json:"failure,omitempty"`
}
type BaseImageScanStore interface {
	PublishBaseImageScan(context.Context, BaseImageScanInput) (BaseImageScan, error)
	// Selection history; this does not assert current bytes, keys or expiry.
	GetCurrentBaseImageScan(context.Context, string) (BaseImageScan, error)
	// Checks the current producer and scan lease using the storage clock. This
	// does not check mutable storage bytes or confer runtime authority.
	GetFreshBaseImageScan(context.Context, string, string) (BaseImageScan, error)
}

func cloneBaseImageScan(value BaseImageScan) BaseImageScan {
	if value.Input.Report != nil {
		report := cloneArtifactScanResult(*value.Input.Report)
		value.Input.Report = &report
	}
	value.Result = cloneArtifactScanResult(value.Result)
	return value
}
func prepareBaseImageScan(input BaseImageScanInput) (BaseImageScanInput, string, error) {
	in := cloneBaseImageScan(BaseImageScan{Input: input}).Input
	if !validStandardResourceRead(in.ID, in.BaseProducerID) || len(in.BaseInputHash) != 64 || !in.Artifact.Valid() || in.SourceReference == "" {
		return in, "", ErrInvalidArgument
	}
	report, err := prepareProducerScanReport(in.Status, in.ScannerName, in.Failure, in.Report, in.SourceReference, in.Artifact.Digest)
	if err != nil {
		return in, "", err
	}
	in.ID, in.BaseProducerID = canonicalStandardUUID(in.ID), canonicalStandardUUID(in.BaseProducerID)
	in.Report = report
	hash, err := standardReviewDigest(in)
	return in, hash, err
}
func baseScanResult(in BaseImageScanInput, at time.Time) api.ScanResult {
	return artifactScanResult(DeploymentArtifactScanInput{Status: in.Status, ImageReference: in.SourceReference, ArtifactDigest: in.Artifact.Digest, Report: in.Report, Failure: in.Failure}, at)
}
func checkBaseScanProducer(in BaseImageScanInput, producer BaseImageProducer) error {
	if err := validateBaseImageProducer(producer); err != nil {
		return err
	}
	if in.BaseProducerID != producer.ID || in.BaseInputHash != producer.InputHash || in.Artifact != producer.Input.Artifact || in.SourceReference != producer.Input.SourceReference {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}
func validateBaseImageScan(value BaseImageScan) error {
	in, hash, err := prepareBaseImageScan(value.Input)
	if err != nil {
		return err
	}
	if value.ID != in.ID || value.InputHash != hash || value.ScannedAt.IsZero() || !value.ExpiresAt.After(value.ScannedAt) || value.ExpiresAt.Sub(value.ScannedAt) > api.ApplicationStandardArtifactScanTTL {
		return fmt.Errorf("base scan stored binding mismatch")
	}
	expected, err := standardReviewDigest(baseScanResult(in, value.ScannedAt))
	if err != nil {
		return err
	}
	actual, err := standardReviewDigest(value.Result)
	if err != nil || expected != actual {
		return fmt.Errorf("base scan stored result mismatch")
	}
	return nil
}

func checkBaseImageScanLease(value BaseImageScan, producer BaseImageProducer, now time.Time) error {
	if err := validateBaseImageScan(value); err != nil {
		return err
	}
	if err := checkBaseScanProducer(value.Input, producer); err != nil {
		return err
	}
	if value.Input.Status != "complete" || value.ScannedAt.Before(producer.PublishedAt) || value.ScannedAt.After(now) || !value.ExpiresAt.After(now) {
		return ErrApplicationStandardRuntimeStale
	}
	return checkProducerScanFreshness(value.Input.Report, now)
}
