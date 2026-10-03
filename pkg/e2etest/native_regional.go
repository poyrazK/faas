package e2etest

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

const NativeRegionalAcceptanceMarker = "/etc/faas/regional-acceptance-host"

// NativeHostCommand runs fixed diagnostic or service arguments on one host.
type NativeHostCommand func(context.Context, ...string) ([]byte, error)

type NativeHostEvidence struct {
	MachineID      string
	Platform       string
	Virtualization string
}

var acceptanceSSHPattern = regexp.MustCompile(`^[a-zA-Z0-9_\[][a-zA-Z0-9_.@:\[\]-]*$`)
var acceptanceMachineIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// NativeAcceptanceSSH rejects targets that could be interpreted as options or
// remote shell syntax. Commands have a bounded deadline and use BatchMode.
func NativeAcceptanceSSH(target string) (NativeHostCommand, error) {
	if !acceptanceSSHPattern.MatchString(target) {
		return nil, errors.New("invalid regional acceptance SSH target")
	}
	return func(ctx context.Context, args ...string) ([]byte, error) {
		commandCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		argv := append([]string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", target}, args...)
		return exec.CommandContext(commandCtx, "ssh", argv...).CombinedOutput()
	}, nil
}

// ProbeNativeAcceptanceHost must pass before a regional fault drill mutates
// the fleet. A KVM device alone does not qualify nested virtualization.
func ProbeNativeAcceptanceHost(ctx context.Context, run NativeHostCommand) (NativeHostEvidence, error) {
	var evidence NativeHostEvidence
	platform, err := run(ctx, "uname", "-sm")
	evidence.Platform = strings.TrimSpace(string(platform))
	if err != nil || evidence.Platform != "Linux x86_64" {
		return evidence, errors.New("regional worker must run Linux x86_64")
	}
	virt, err := run(ctx, "systemd-detect-virt")
	evidence.Virtualization = strings.TrimSpace(string(virt))
	var exit interface{ ExitCode() int }
	// systemd reports absence with "none" and exit 1. Missing commands,
	// transport failures and any detected VM/container refuse qualification.
	if evidence.Virtualization != "none" || !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return evidence, errors.New("regional worker must be a native host; virtualization probe did not confirm absence")
	}
	if _, err := run(ctx, "sudo", "-n", "test", "-c", "/dev/kvm"); err != nil {
		return evidence, errors.New("regional worker has no KVM device")
	}
	if _, err := run(ctx, "sudo", "-n", "test", "-f", NativeRegionalAcceptanceMarker); err != nil {
		return evidence, errors.New("regional worker is not designated for acceptance")
	}
	machine, err := run(ctx, "cat", "/etc/machine-id")
	evidence.MachineID = strings.TrimSpace(string(machine))
	if err != nil || !acceptanceMachineIDPattern.MatchString(evidence.MachineID) {
		return evidence, errors.New("regional worker has no valid machine identity")
	}
	return evidence, nil
}

// JobBootOperations counts all JobColdBoot outcomes, including refused calls.
// The always-present vmmd release gauge guards against an empty/wrong scrape.
func JobBootOperations(data string) (float64, error) {
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(data))
	if err != nil {
		return 0, fmt.Errorf("parse vmmd metrics: %w", err)
	}
	release := families["vmmd_faas_deploy_version"]
	if release == nil || len(release.Metric) == 0 || release.Metric[0].GetGauge().GetValue() != 1 {
		return 0, errors.New("metrics do not identify a running vmmd")
	}
	var total float64
	for _, metric := range families["vmmd_ops_total"].GetMetric() {
		for _, label := range metric.Label {
			if label.GetName() != "op" || label.GetValue() != "JobColdBoot" {
				continue
			}
			value := metric.GetCounter().GetValue()
			if metric.Counter == nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || math.Trunc(value) != value {
				return 0, errors.New("invalid JobColdBoot counter")
			}
			total += value
		}
	}
	return total, nil
}
