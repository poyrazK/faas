package fcvm_test

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/imaged"
)

const (
	acceptanceScanFailOnEnv    = "FAAS_METAL_SCAN_FAIL_ON"
	acceptanceScanOnlyFixedEnv = "FAAS_METAL_SCAN_ONLY_FIXED"
)

type acceptanceScanPolicy struct {
	FailOn    string
	OnlyFixed bool
}

func acceptanceScanPolicyFromEnv() (acceptanceScanPolicy, error) {
	failOn := strings.ToUpper(strings.TrimSpace(os.Getenv(acceptanceScanFailOnEnv)))
	if failOn == "" {
		failOn = imaged.SeverityHigh
	}
	if severityRank(failOn) == 0 {
		return acceptanceScanPolicy{}, fmt.Errorf("%s must be one of critical, high, medium, or low", acceptanceScanFailOnEnv)
	}

	onlyFixed := true
	if raw := strings.TrimSpace(os.Getenv(acceptanceScanOnlyFixedEnv)); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return acceptanceScanPolicy{}, fmt.Errorf("%s must be a boolean: %w", acceptanceScanOnlyFixedEnv, err)
		}
		onlyFixed = value
	}
	return acceptanceScanPolicy{FailOn: failOn, OnlyFixed: onlyFixed}, nil
}

func (p acceptanceScanPolicy) violations(result imaged.ScanResult) []imaged.Vulnerability {
	threshold := severityRank(p.FailOn)
	violations := make([]imaged.Vulnerability, 0)
	for _, vulnerability := range result.Vulnerabilities {
		if severityRank(vulnerability.Severity) < threshold {
			continue
		}
		if p.OnlyFixed && strings.TrimSpace(vulnerability.FixedIn) == "" {
			continue
		}
		violations = append(violations, vulnerability)
	}
	return violations
}

func severityRank(severity string) int {
	switch strings.ToUpper(strings.TrimSpace(severity)) {
	case imaged.SeverityCritical:
		return 4
	case imaged.SeverityHigh:
		return 3
	case imaged.SeverityMedium:
		return 2
	case imaged.SeverityLow:
		return 1
	default:
		return 0
	}
}
