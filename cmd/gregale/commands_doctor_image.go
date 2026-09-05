package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/statefuldenylist"
)

const doctorUsage = "usage: gregale doctor [--strict] [--json] [path] | --image REF [--registry-user USER --registry-password-stdin]"

type doctorImageInspector interface {
	InspectImage(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error)
}

type doctorImageFlags struct {
	image, user   string
	passwordStdin bool
}

func registerDoctorImageFlags(fs *flag.FlagSet) *doctorImageFlags {
	f := &doctorImageFlags{}
	fs.StringVar(&f.image, "image", "", "inspect OCI image metadata without downloading layers")
	fs.StringVar(&f.user, "registry-user", "", "registry username (requires --registry-password-stdin)")
	fs.BoolVar(&f.passwordStdin, "registry-password-stdin", false, "read registry password or token from stdin")
	return f
}

func (f *doctorImageFlags) validate(fs *flag.FlagSet) error {
	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	if fs.NArg() > 1 || (f.image != "" && fs.NArg() != 0) {
		return errors.New("choose one source path or --image REF; put flags before the source path")
	}
	if visited["image"] {
		if _, err := reference.ParseNormalizedNamed(f.image); err != nil {
			return errors.New("invalid image reference; use registry/repository:tag or registry/repository@sha256:digest")
		}
	}
	if visited["registry-user"] || visited["registry-password-stdin"] {
		if f.image == "" || f.user == "" || !f.passwordStdin {
			return errors.New("registry credentials require --image, --registry-user and --registry-password-stdin together")
		}
		if len(f.user) > api.MaxRegistryUsernameLen {
			return errors.New("registry username exceeds the platform size limit")
		}
	}
	return nil
}

// doctorImage deliberately omits image environment values and credentials.
// EffectiveArgv is the unmodified image command, before deployment overrides.
type doctorImage struct {
	Reference     string                      `json:"reference"`
	Digest        string                      `json:"digest,omitempty"`
	OS            string                      `json:"os,omitempty"`
	Architecture  string                      `json:"architecture,omitempty"`
	Entrypoint    []string                    `json:"entrypoint,omitempty"`
	Command       []string                    `json:"command,omitempty"`
	EffectiveArgv []string                    `json:"effective_argv,omitempty"`
	User          string                      `json:"user,omitempty"`
	WorkingDir    string                      `json:"working_dir,omitempty"`
	StopSignal    string                      `json:"stop_signal,omitempty"`
	Healthcheck   *api.AppManifestHealthcheck `json:"healthcheck,omitempty"`
	ExposedPorts  []string                    `json:"exposed_ports,omitempty"`
}

func runDoctorImageCommand(flags *doctorImageFlags, inspector doctorImageInspector, strict, asJSON bool) int {
	var auth *oci.BasicAuth
	if flags.passwordStdin {
		// Reuse the maximum credential size accepted by the platform.
		password, err := io.ReadAll(io.LimitReader(osStdin, int64(api.MaxRegistryPasswordBytes)+1))
		if err != nil || len(password) > api.MaxRegistryPasswordBytes || len(strings.TrimSpace(string(password))) == 0 {
			_, _ = fmt.Fprintln(osStderr, "cannot read a nonempty registry credential within the platform size limit")
			return 2
		}
		auth = &oci.BasicAuth{Username: flags.user, Password: strings.TrimRight(string(password), "\r\n")}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(api.OCIPullTimeoutSeconds)*time.Second)
	defer cancel()
	report := runDoctorImageChecks(ctx, flags.image, auth, inspector)
	if asJSON {
		if err := writeJSON(report); err != nil {
			return 1
		}
	} else {
		renderDoctorHuman(osStdout, report)
	}
	if report.HasErrors() || (strict && report.HasWarnings()) {
		return 1
	}
	return 0
}

func runDoctorImageChecks(ctx context.Context, ref string, auth *oci.BasicAuth, inspector doctorImageInspector) doctorReport {
	rep := doctorReport{Image: &doctorImage{Reference: ref}, Checks: []doctorCheck{}}
	if hint, denied := statefuldenylist.Match(ref); denied {
		rep.Checks = append(rep.Checks, doctorCheck{Name: "stateless-only", Status: "error", Code: api.CodeStatelessOnlyViolation, Hint: hint, Fix: "Use a stateless application image and an external data store."})
	} else {
		rep.Checks = append(rep.Checks, doctorCheck{Name: "stateless-only", Status: "ok"})
	}
	result, err := inspector.InspectImage(ctx, ref, auth)
	if err != nil {
		rep.Checks = append(rep.Checks, doctorImageAccessError(err))
		return rep
	}
	rep.Image = describeDoctorImage(result)
	rep.Checks = append(rep.Checks, doctorCheck{Name: "registry-metadata", Status: "ok"})
	rep.Checks = append(rep.Checks, doctorImagePlatformCheck(result.Config))
	rep.Checks = append(rep.Checks, doctorImageContractCheck(result.Config))
	rep.Checks = append(rep.Checks, doctorImageHealthcheck(result.Config.Healthcheck))
	rep.Checks = append(rep.Checks, doctorImageStopSignal(result.Config.StopSignal))
	if len(result.Config.Volumes) > 0 {
		rep.Checks = append(rep.Checks, doctorCheck{Name: "volumes", Status: "warn", Hint: "Image VOLUME declarations do not provision persistent storage.", Fix: "Keep durable state in an external data store."})
	}
	for _, name := range []string{"startup-and-port", "filesystem-and-user", "plan-and-deployment-policy"} {
		rep.Checks = append(rep.Checks, doctorCheck{Name: name, Status: "skipped", Reason: "Metadata inspection cannot verify running processes, layer contents, or account/deployment settings. Deployment validation remains authoritative."})
	}
	return rep
}

func doctorImageAccessError(err error) doctorCheck {
	c := doctorCheck{Name: "registry-metadata", Status: "error", Code: "image_inspection_failed", Hint: "Could not read image metadata from this machine.", Fix: "Check the image reference, registry connectivity and pull permissions; supply private-registry credentials with --registry-user and --registry-password-stdin."}
	// Never print arbitrary registry response bodies: they can echo credentials.
	if errors.Is(err, oci.ErrImageManifestInvalid) {
		c.Code, c.Hint = api.CodeImageManifestInvalid, "The image metadata does not meet the deployment manifest contract."
		c.Fix = "Use a valid single-platform Linux/amd64 image digest. Manifest lists/indexes must be pinned to their platform-specific child manifest; rebuild malformed images."
	} else if errors.Is(err, context.DeadlineExceeded) {
		c.Code, c.Hint = "image_inspection_timeout", "Registry metadata inspection timed out."
	}
	return c
}

func doctorImagePlatformCheck(cfg oci.ImageConfig) doctorCheck {
	if cfg.OS != "linux" || cfg.Architecture != "amd64" {
		return doctorCheck{Name: "platform", Status: "error", Code: api.CodeAppArchMismatch, Hint: "Image must declare os=linux and architecture=amd64 for production Gregale.", Fix: "Build for linux/amd64 and inspect that platform's image digest."}
	}
	return doctorCheck{Name: "platform", Status: "ok"}
}

func doctorImageContractCheck(cfg oci.ImageConfig) doctorCheck {
	_, err := oci.ManifestFromConfig(oci.Config{Entrypoint: cfg.Entrypoint, Cmd: cfg.Cmd, WorkingDir: cfg.WorkingDir, User: cfg.User, Healthcheck: cfg.Healthcheck, StopSignal: cfg.StopSignal, StopGracePeriodS: cfg.StopGracePeriodS})
	if err != nil {
		return doctorCheck{Name: "runtime-contract", Status: "error", Code: api.CodeImageManifestInvalid, Hint: err.Error(), Fix: "Provide a valid ENTRYPOINT/CMD and lifecycle metadata. This check evaluates the image before deployment overrides."}
	}
	return doctorCheck{Name: "runtime-contract", Status: "ok"}
}

func doctorImageHealthcheck(hc *oci.ImageHealthcheck) doctorCheck {
	if hc == nil || len(hc.Test) == 0 || hc.Test[0] == "NONE" {
		return doctorCheck{Name: "healthcheck", Status: "skipped", Reason: "No enabled image HEALTHCHECK; application readiness still needs runtime verification."}
	}
	validCommand := (hc.Test[0] == "CMD" && len(hc.Test) > 1 && hc.Test[1] != "") || (hc.Test[0] == "CMD-SHELL" && len(hc.Test) == 2 && strings.TrimSpace(hc.Test[1]) != "")
	if !validCommand || hc.IntervalS < 0 || hc.TimeoutS < 0 || hc.StartPeriodS < 0 || hc.Retries < 0 {
		return doctorCheck{Name: "healthcheck", Status: "warn", Hint: "HEALTHCHECK has an invalid command shape or negative timing/retry values.", Fix: "Use CMD with an executable or CMD-SHELL with one command string, and nonnegative timing/retry values."}
	}
	return doctorCheck{Name: "healthcheck", Status: "ok"}
}

func doctorImageStopSignal(signal string) doctorCheck {
	// Guest-init's parseStopSignal accepts these forms; other values fall
	// back to SIGTERM. Report the fallback without changing runtime policy.
	switch strings.ToUpper(strings.TrimSpace(signal)) {
	case "", "SIGTERM", "TERM", "15", "SIGINT", "INT", "2", "SIGQUIT", "QUIT", "3", "SIGHUP", "HUP", "1", "SIGUSR1", "USR1", "10", "SIGUSR2", "USR2", "12":
		return doctorCheck{Name: "stop-signal", Status: "ok"}
	default:
		return doctorCheck{Name: "stop-signal", Status: "warn", Hint: "Guest-init does not recognize this STOPSIGNAL and falls back to SIGTERM.", Fix: "Use SIGTERM, SIGINT, SIGQUIT, SIGHUP, SIGUSR1 or SIGUSR2."}
	}
}

func describeDoctorImage(result oci.ImageInspection) *doctorImage {
	cfg := result.Config
	m := &doctorImage{Reference: result.Reference, Digest: result.Digest, OS: cfg.OS, Architecture: cfg.Architecture, Entrypoint: cfg.Entrypoint, Command: cfg.Cmd, EffectiveArgv: append(append([]string{}, cfg.Entrypoint...), cfg.Cmd...), User: cfg.User, WorkingDir: cfg.WorkingDir, StopSignal: cfg.StopSignal}
	if hc := cfg.Healthcheck; hc != nil {
		m.Healthcheck = &api.AppManifestHealthcheck{Test: hc.Test, IntervalS: hc.IntervalS, TimeoutS: hc.TimeoutS, Retries: hc.Retries, StartPeriodS: hc.StartPeriodS}
	}
	if m.User == "" || m.User == "1000" {
		m.User = api.DefaultAppUser
	}
	if manifest, err := oci.ManifestFromConfig(oci.Config{Entrypoint: cfg.Entrypoint, Cmd: cfg.Cmd, User: cfg.User}); err == nil {
		m.User = manifest.EffectiveUser()
	}
	if m.WorkingDir == "" {
		m.WorkingDir = "/"
	}
	if m.StopSignal == "" {
		m.StopSignal = "SIGTERM"
	}
	for port := range cfg.ExposedPorts {
		m.ExposedPorts = append(m.ExposedPorts, port)
	}
	sort.Strings(m.ExposedPorts)
	return m
}

func renderDoctorImage(w io.Writer, img *doctorImage) {
	_, _ = fmt.Fprintf(w, "gregale doctor — image %q\n", img.Reference)
	if img.Digest == "" {
		return
	}
	_, _ = fmt.Fprintf(w, "  platform: %q\n  entrypoint: %q\n  command: %q\n  effective argv: %q\n  user: %q  working directory: %q\n  stop signal: %q\n  declared ports: %q\n", img.OS+"/"+img.Architecture, img.Entrypoint, img.Command, img.EffectiveArgv, img.User, img.WorkingDir, img.StopSignal, img.ExposedPorts)
	if img.Healthcheck != nil {
		_, _ = fmt.Fprintf(w, "  healthcheck: %q\n", img.Healthcheck.Test)
	}
	_, _ = fmt.Fprintln(w, "Metadata checks only; no layers downloaded or containers executed. Values precede deployment overrides.")
}
