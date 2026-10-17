package objectqualification

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

type PreflightOptions struct {
	Daemon, Control, Directory string
	Remote                     RemoteOptions
}

// PreflightReport records readiness observations, never object-write outcomes
// or independently verified physical storage failure domains.
type PreflightReport struct {
	Version                        int             `json:"version"`
	Ready                          bool            `json:"ready"`
	StartedAt                      time.Time       `json:"started_at"`
	FinishedAt                     time.Time       `json:"finished_at"`
	Scope                          string          `json:"scope"`
	Primary                        *HostInspection `json:"primary,omitempty"`
	Follower                       *HostInspection `json:"follower,omitempty"`
	DistinctKernelBoots            bool            `json:"distinct_kernel_boots"`
	PhysicalFailureDomainsVerified bool            `json:"physical_failure_domains_verified"`
	Checks                         []Check         `json:"checks"`
	Error                          string          `json:"error,omitempty"`
}

// Preflight inspects existing parents and executables without creating fixture
// data. SSH host access is operator-configured; this does not provision hosts.
func Preflight(ctx context.Context, o PreflightOptions) (report PreflightReport, err error) {
	report = PreflightReport{Version: 1, StartedAt: time.Now().UTC(), Scope: "read-only qualification readiness; no node data, credentials or listeners created; kernel and local-block filesystem observations; physical failure domains unverified", Checks: []Check{}}
	defer func() {
		report.FinishedAt = time.Now().UTC()
		report.Ready = err == nil
		if err != nil {
			report.Error = err.Error()
		}
	}()
	if o.Daemon == "" || o.Control == "" || o.Directory == "" {
		return report, errors.New("preflight requires --daemon, --ctl and --work-dir")
	}
	for _, path := range []*string{&o.Daemon, &o.Control, &o.Directory} {
		if *path, err = filepath.Abs(*path); err != nil {
			return report, err
		}
	}
	if o.Remote.AllowSharedHost {
		return report, errors.New("independent-host preflight does not permit --allow-shared-host")
	}
	localRequest := inspectionRequest{Directory: o.Directory, Daemon: o.Daemon, Control: o.Control, Role: "primary"}
	local, localErr := inspectHost(ctx, localRequest)
	report.Primary = &local
	if localErr == nil {
		localErr = validateHostInspection(local, localRequest)
	}
	addPreflightCheck(&report, "primary_inspection", localErr)
	addPreflightCheck(&report, "primary_storage", inspectionStorageCheck(local, localErr))
	remoteRequest := inspectionRequest{Directory: o.Remote.Directory, Daemon: o.Remote.Daemon, Control: o.Remote.Control, Role: "follower"}
	remote, remoteErr := inspectRemote(ctx, o.Remote, remoteRequest)
	if remote.Version != 0 {
		report.Follower = &remote
	}
	addPreflightCheck(&report, "follower_inspection", remoteErr)
	addPreflightCheck(&report, "follower_storage", inspectionStorageCheck(remote, remoteErr))
	compareInspectionHosts(&report, localErr == nil && remoteErr == nil)
	var problems []error
	for _, check := range report.Checks {
		if !check.Passed {
			problems = append(problems, fmt.Errorf("%s: %s", check.Name, check.Error))
		}
	}
	return report, errors.Join(problems...)
}

func addPreflightCheck(report *PreflightReport, name string, err error) {
	check := Check{Name: name, Passed: err == nil}
	if err != nil {
		check.Error = err.Error()
	}
	report.Checks = append(report.Checks, check)
}

func inspectionStorageCheck(host HostInspection, inspectionErr error) error {
	if inspectionErr != nil {
		return errors.New("complete filesystem inspection unavailable")
	}
	return validateInspectionStorage(host.Storage)
}

func compareInspectionHosts(report *PreflightReport, complete bool) {
	if !complete || report.Primary == nil || report.Follower == nil {
		addPreflightCheck(report, "distinct_kernel_boots", errors.New("complete host identities unavailable"))
		addPreflightCheck(report, "matching_executables", errors.New("complete executable identities unavailable"))
		return
	}
	primary, follower := report.Primary, report.Follower
	report.DistinctKernelBoots = primary.KernelBootSHA256 != follower.KernelBootSHA256
	var separation, executables error
	if !report.DistinctKernelBoots {
		separation = errors.New("primary and follower share a kernel boot identity")
	}
	if primary.Platform != follower.Platform || primary.DaemonSHA256 != follower.DaemonSHA256 || primary.ControlSHA256 != follower.ControlSHA256 || primary.AgentSHA256 != follower.AgentSHA256 {
		executables = errors.New("primary and follower qualification, daemon and control executables must match")
	}
	addPreflightCheck(report, "distinct_kernel_boots", separation)
	addPreflightCheck(report, "matching_executables", executables)
}

func WritePreflightReport(path string, report PreflightReport) error {
	return writeQualificationEvidence(path, report)
}
