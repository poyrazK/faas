package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Claims contain opaque fencing tokens. Each retry gets a different lease.
type AutomaticRouteCheckClaim struct {
	DeploymentID string    `json:"deployment_id"`
	AppID        string    `json:"app_id"`
	AccountID    string    `json:"account_id"`
	RequestID    string    `json:"request_id"`
	LeaseToken   string    `json:"lease_token"`
	LeaseUntil   time.Time `json:"lease_until"`
	Attempts     int       `json:"attempts"`
}

// Fingerprints must be pure and share the evaluator's configuration digest.
type RouteCheckFingerprinter func(RoutePolicySnapshot) string

type AutomaticRouteCheckStore interface {
	QueueAutomaticRouteCheck(context.Context, string, string, string) error
	ClaimAutomaticRouteCheck(context.Context, time.Duration) (AutomaticRouteCheckClaim, error)
	CompleteAutomaticRouteCheck(context.Context, AutomaticRouteCheckClaim, api.RouteRequirementsCheck, string, bool) (bool, error)
	FailAutomaticRouteCheck(context.Context, AutomaticRouteCheckClaim) (bool, error)
	GetAutomaticRouteCheck(context.Context, string, string, string, RouteCheckFingerprinter) (api.AutomaticRouteCheck, error)
}

var ErrAutomaticRouteCheckPlan = errors.New("stored route checks require captured contract entitlement")

var (
	_ AutomaticRouteCheckStore = (*MemStore)(nil)
	_ AutomaticRouteCheckStore = (*PgStore)(nil)
)

type automaticRouteCheckRecord struct {
	api.AutomaticRouteCheck
	CaptureSHA256    string `json:"input_capture_sha256"`
	CaptureTruncated bool   `json:"input_capture_truncated"`
}

func automaticRouteCheckView(record automaticRouteCheckRecord, snapshot RoutePolicySnapshot, saved api.SavedRouteRequirements, fingerprint RouteCheckFingerprinter) (api.AutomaticRouteCheck, error) {
	if snapshot.Account.Plan.OpenAPIDocsPerDeployment() <= 0 {
		return api.AutomaticRouteCheck{}, ErrAutomaticRouteCheckPlan
	}
	result := record.AutomaticRouteCheck
	result.CurrentRequirementsRevision, result.CurrentRequirementsSHA256 = saved.Revision, saved.SHA256
	result.StaleReasons = []string{}
	if result.Check == nil {
		if result.State == "complete" {
			return result, errors.New("completed route check has no evidence")
		}
		result.Freshness = "unavailable"
		return result, nil
	}
	if _, err := encodeAutomaticRouteCheck(AutomaticRouteCheckClaim{AppID: snapshot.App.ID, DeploymentID: result.DeploymentID}, *result.Check, record.CaptureSHA256, record.CaptureTruncated); err != nil {
		return result, err
	}
	if saved.Revision != result.Check.RequirementsRevision || saved.SHA256 != result.Check.RequirementsSHA256 {
		result.StaleReasons = append(result.StaleReasons, "requirements_changed")
	}
	captureSHA, truncated := RouteCheckCaptureIdentity(snapshot.Contract)
	if captureSHA != record.CaptureSHA256 || truncated != record.CaptureTruncated {
		result.StaleReasons = append(result.StaleReasons, "capture_changed")
	}
	if fingerprint == nil || fingerprint(snapshot) != result.Check.ConfigurationSHA256 {
		result.StaleReasons = append(result.StaleReasons, "configuration_changed")
	}
	result.Freshness = "current"
	if len(result.StaleReasons) > 0 {
		result.Freshness = "stale"
	}
	return result, nil
}

func RouteCheckCaptureIdentity(contract *RoutePolicyContract) (string, bool) {
	if contract == nil {
		return "", false
	}
	return contract.SHA256, contract.Truncated
}

func routeCheckRetry(attempts int) time.Duration {
	exponent := min(max(attempts-1, 0), 9)
	return min(time.Second*time.Duration(1<<exponent), api.RouteCheckRetryMax)
}

func routeCheckSHA(value string) bool {
	return len(value) == 64 && strings.IndexFunc(value, func(r rune) bool { return !strings.ContainsRune("0123456789abcdef", r) }) < 0
}

func encodeAutomaticRouteCheck(claim AutomaticRouteCheckClaim, check api.RouteRequirementsCheck, captureSHA string, truncated bool) ([]byte, error) {
	if check.Version != 1 || check.AppID != claim.AppID || check.DeploymentID != claim.DeploymentID || check.App == "" || check.RequirementsRevision < 1 || check.RequirementsRevision > api.RouteRequirementsMaxRevision || !routeCheckSHA(check.RequirementsSHA256) || !routeCheckSHA(check.ConfigurationSHA256) || check.Report.Version != 2 || check.Report.SHA256 != check.RequirementsSHA256 || check.Report.Coverage == nil || captureSHA != "" && !routeCheckSHA(captureSHA) {
		return nil, errors.New("automatic route check identity or provenance is invalid")
	}
	coverage := check.Report.Coverage
	if coverage.Status == "available" {
		if truncated || coverage.SHA256 != captureSHA || !routeCheckSHA(captureSHA) || coverage.Deployment != claim.DeploymentID || coverage.RouteCount < 1 {
			return nil, errors.New("automatic route check capture provenance is invalid")
		}
	} else if coverage.Status != "unavailable" || check.Report.Status != "unknown" {
		return nil, errors.New("unavailable automatic route evidence cannot pass")
	}
	if check.Report.Status != "satisfied" && check.Report.Status != "violated" && check.Report.Status != "unknown" {
		return nil, errors.New("automatic route check verdict is invalid")
	}
	body, err := json.Marshal(check)
	if err == nil && len(body) > api.RouteCheckMaxResultBytes {
		err = errors.New("automatic route check result exceeds the supported limit")
	}
	return body, err
}
