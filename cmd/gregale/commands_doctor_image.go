package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/statefuldenylist"
)

const doctorUsage = "usage: gregale doctor [--strict] [--json] [--app SLUG] [--path DIR | DIR] | --image REF [--registry-user USER --registry-password-stdin | --docker-config PATH] [--boot [--env-file FILE]]"

type doctorImageInspector interface {
	InspectImage(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error)
}

type doctorImageFlags struct {
	image, user   string
	passwordStdin bool
	// dockerConfig names a Docker config whose credentials for the image's
	// registry the inspection uses, credential helpers included.
	dockerConfig string
	// boot runs the image with the local Docker client and checks that it
	// starts, serves HTTP on 0.0.0.0:$PORT and stays up (doctor_boot.go).
	boot    bool
	envFile string
	// ramMB overrides the boot's memory limit; app is doctor's --app, whose
	// RAM is the limit otherwise (doctorBootMemory).
	ramMB int
	// healthPath is the readiness path --boot probes instead of /.
	healthPath string
	app        string
}

func registerDoctorImageFlags(fs *flag.FlagSet) *doctorImageFlags {
	f := &doctorImageFlags{}
	fs.StringVar(&f.image, "image", "", "inspect OCI image metadata without downloading layers")
	fs.StringVar(&f.user, "registry-user", "", "registry username (requires --registry-password-stdin)")
	fs.BoolVar(&f.passwordStdin, "registry-password-stdin", false, "read registry password or token from stdin")
	fs.StringVar(&f.dockerConfig, "docker-config", "", "use the image registry's credentials from this Docker config.json or its directory")
	fs.BoolVar(&f.boot, "boot", false, "with --image, run the image with local Docker and check it starts, serves HTTP on 0.0.0.0:$PORT and stays up")
	fs.StringVar(&f.envFile, "env-file", "", "with --boot, pass KEY=VALUE lines from this file to the container, docker env-file format")
	fs.IntVar(&f.ramMB, "ram", 0, "with --boot, memory limit in MB (default: --app's RAM, else your plan's default for a new app)")
	fs.StringVar(&f.healthPath, "healthcheck-path", "", "with --boot, probe this readiness path instead of / and require 2xx, as Gregale does (for example /healthz)")
	return f
}

func (f *doctorImageFlags) validate(fs *flag.FlagSet) error {
	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	if fs.NArg() > 1 || (f.image != "" && fs.NArg() != 0) {
		return errors.New("choose one source path or --image REF")
	}
	if visited["image"] {
		if _, err := reference.ParseNormalizedNamed(f.image); err != nil {
			return errors.New("invalid image reference; use registry/repository:tag or registry/repository@sha256:digest")
		}
	}
	if visited["docker-config"] {
		if f.image == "" || strings.TrimSpace(f.dockerConfig) == "" {
			return errors.New("--docker-config requires --image and a Docker config path")
		}
		if visited["registry-user"] || visited["registry-password-stdin"] {
			return errors.New("--docker-config cannot be combined with --registry-user: choose one source of registry credentials")
		}
	}
	if visited["boot"] && f.boot && f.image == "" {
		return errors.New("--boot requires --image: it runs that image with your local Docker")
	}
	if visited["env-file"] && (!f.boot || strings.TrimSpace(f.envFile) == "") {
		return errors.New("--env-file requires --boot and a file path")
	}
	if visited["healthcheck-path"] && (!f.boot || !strings.HasPrefix(f.healthPath, "/") || strings.ContainsFunc(f.healthPath, func(r rune) bool { return r <= ' ' || r == 0x7f })) {
		return errors.New("--healthcheck-path requires --boot and an HTTP path that starts with / and has no spaces")
	}
	// Docker itself refuses a limit below its minimum, with its own message.
	if visited["ram"] && (!f.boot || f.ramMB <= 0) {
		return errors.New("--ram requires --boot and a positive memory limit in MB")
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
	InputReference  string                      `json:"input_reference,omitempty"`
	SourceReference string                      `json:"source_reference,omitempty"`
	Reference       string                      `json:"reference"`
	Digest          string                      `json:"digest,omitempty"`
	OS              string                      `json:"os,omitempty"`
	Architecture    string                      `json:"architecture,omitempty"`
	Entrypoint      []string                    `json:"entrypoint,omitempty"`
	Command         []string                    `json:"command,omitempty"`
	EffectiveArgv   []string                    `json:"effective_argv,omitempty"`
	User            string                      `json:"user,omitempty"`
	WorkingDir      string                      `json:"working_dir,omitempty"`
	StopSignal      string                      `json:"stop_signal,omitempty"`
	Healthcheck     *api.AppManifestHealthcheck `json:"healthcheck,omitempty"`
	ServingPort     int                         `json:"serving_port,omitempty"`
	ExposedPorts    []string                    `json:"exposed_ports,omitempty"`
	VolumePaths     []string                    `json:"volume_paths,omitempty"`
	// Layers summarizes compressed layer sizes from the image manifest.
	Layers *doctorImageLayers `json:"layers,omitempty"`
	// Source is "local-docker" when the registry could not serve the image
	// and the metadata came from the local Docker store; Digest is then the
	// local image ID.
	Source string `json:"source,omitempty"`
	// Booted is set when --boot ran the image with local Docker.
	Booted bool `json:"booted,omitempty"`
}

func runDoctorImageCommand(flags *doctorImageFlags, inspector doctorImageInspector, strict, asJSON bool) int {
	var auth *oci.BasicAuth
	if flags.passwordStdin {
		// Reuse the maximum credential size accepted by the platform.
		password, err := io.ReadAll(io.LimitReader(osStdin, int64(api.MaxRegistryPasswordBytes)+1))
		if err != nil || len(password) > api.MaxRegistryPasswordBytes || len(strings.TrimSpace(string(password))) == 0 {
			PrintUsage(osStderr, "cannot read a nonempty registry credential within the platform size limit", "doctor")
			return 1
		}
		auth = &oci.BasicAuth{Username: flags.user, Password: strings.TrimRight(string(password), "\r\n")}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(api.OCIPullTimeoutSeconds)*time.Second)
	defer cancel()
	if flags.dockerConfig != "" {
		cred, err := dockerConfigRegistryAuth(ctx, flags.dockerConfig, flags.image, runDockerCredentialHelper)
		if err != nil {
			return printErr("Could not read registry credentials", err)
		}
		auth = &oci.BasicAuth{Username: cred.Username, Password: cred.Password}
	}
	report := runDoctorImageChecksWithLocal(ctx, flags.image, auth, inspector, readLocalDockerImage)
	if flags.boot {
		addDoctorBootChecks(context.WithoutCancel(ctx), &report, flags) // not bound by the registry inspection timeout
	}
	if asJSON {
		if err := writeJSON(report); err != nil {
			return 1
		}
	} else {
		renderDoctorHumanExit(osStdout, report, report.HasErrors() || (strict && report.HasWarnings()))
	}
	if report.HasErrors() || (strict && report.HasWarnings()) {
		return 1
	}
	return 0
}

func runDoctorImageChecks(ctx context.Context, ref string, auth *oci.BasicAuth, inspector doctorImageInspector) doctorReport {
	return runDoctorImageChecksWithLocal(ctx, ref, auth, inspector, nil)
}

// runDoctorImageChecksWithLocal falls back to local, when not nil, if the
// registry cannot serve ref (doctor_local_image.go).
func runDoctorImageChecksWithLocal(ctx context.Context, ref string, auth *oci.BasicAuth, inspector doctorImageInspector, local doctorLocalImageReader) doctorReport {
	rep := doctorReport{Image: &doctorImage{Reference: ref}, Checks: []doctorCheck{}}
	if hint, denied := statefuldenylist.Match(ref); denied {
		rep.Checks = append(rep.Checks, doctorCheck{Name: "stateless-only", Status: "error", Code: api.CodeStatelessOnlyViolation, Hint: hint, Fix: "Use a stateless application image and an external data store."})
	} else {
		rep.Checks = append(rep.Checks, doctorCheck{Name: "stateless-only", Status: "ok"})
	}
	result, err := inspector.InspectImage(ctx, ref, auth)
	if err != nil {
		access := doctorImageAccessError(err)
		if local == nil || !doctorLocalImageFallback(err) {
			rep.Checks = append(rep.Checks, access)
			return rep
		}
		img, ok := local(ctx, ref)
		if !ok {
			rep.Checks = append(rep.Checks, access)
			return rep
		}
		rep.Image = describeDoctorLocalImage(ref, img)
		rep.Checks = append(rep.Checks, doctorLocalRegistryCheck(access, ref))
		rep.Checks = append(rep.Checks, doctorImageConfigChecks(img.Config)...)
		rep.Checks = append(rep.Checks, doctorLocalImageSize(ctx, img.SizeBytes))
		rep.Checks = append(rep.Checks, doctorImageTailChecks(img.Config)...)
		return rep
	}
	rep.Image = describeDoctorImage(result)
	rep.Checks = append(rep.Checks, doctorCheck{Name: "registry-metadata", Status: "ok"})
	rep.Checks = append(rep.Checks, doctorImageConfigChecks(result.Config)...)
	sizeCheck, layers := doctorImageSize(ctx, result)
	rep.Image.Layers = layers
	rep.Checks = append(rep.Checks, sizeCheck)
	rep.Checks = append(rep.Checks, doctorImageTailChecks(result.Config)...)
	return rep
}

// doctorImageConfigChecks are the checks an image config alone answers.
func doctorImageConfigChecks(cfg oci.ImageConfig) []doctorCheck {
	return []doctorCheck{doctorImagePlatformCheck(cfg), doctorImageContractCheck(cfg), doctorImageListenerCheck(cfg), doctorImageHealthcheck(cfg.Healthcheck), doctorImageStopSignal(cfg.StopSignal)}
}

// doctorImageTailChecks are the volume warning and the checks metadata
// cannot answer.
func doctorImageTailChecks(cfg oci.ImageConfig) []doctorCheck {
	var checks []doctorCheck
	if len(cfg.Volumes) > 0 {
		checks = append(checks, doctorCheck{Name: "volumes", Status: "warn", Hint: "Image VOLUME declarations do not provision persistent storage. Snapshots are a startup cache; filesystem contents are not guaranteed across instances, cold fallback, redeployment or node failure.", Fix: "Keep durable state in object storage or an external database; use local paths only for disposable data."})
	}
	for _, name := range []string{"startup-and-port", "filesystem-and-user", "plan-and-deployment-policy"} {
		checks = append(checks, doctorCheck{Name: name, Status: "skipped", Reason: "Metadata inspection cannot verify running processes, unpacked layer contents and filesystem overhead, or account/deployment settings; image-size is an estimate. Deployment validation remains authoritative."})
	}
	return checks
}

// addDoctorBootChecks runs the image locally and replaces the skipped
// startup-and-port check with what the run found. It boots even when registry
// metadata could not be read from the registry or the local Docker store;
// PORT is then the default serving port.
func addDoctorBootChecks(parent context.Context, report *doctorReport, flags *doctorImageFlags) {
	port := api.DefaultAppPort
	if report.Image != nil && report.Image.ServingPort > 0 {
		port = report.Image.ServingPort
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt)
	defer stop()
	app := doctorBootLookupApp(ctx, flags.app)
	memoryMB, memorySource := doctorBootMemory(ctx, flags, app)
	opts := doctorBootOptions{image: flags.image, port: port, envFile: flags.envFile, memoryMB: memoryMB, memorySource: memorySource, healthPath: flags.healthPath, probeHost: app.host}
	if report.Image != nil {
		opts.imageHealthcheck = doctorBootImageHealthcheck(report.Image.Healthcheck)
	}
	boot := runDoctorBootChecks(ctx, opts)
	checks := report.Checks[:0]
	for _, c := range report.Checks {
		if c.Name != "startup-and-port" {
			checks = append(checks, c)
		}
	}
	report.Checks = append(checks, boot...)
	if report.Image != nil {
		report.Image.Booted = true
	}
}

func doctorImageAccessError(err error) doctorCheck {
	c := doctorCheck{Name: "registry-metadata", Status: "error", Code: "image_inspection_failed", Hint: "Could not read image metadata from this machine.", Fix: "Check the image reference, registry connectivity and pull permissions; supply private-registry credentials with --docker-config ~/.docker, or --registry-user and --registry-password-stdin."}
	// Never print arbitrary registry response bodies: they can echo credentials.
	var selection *oci.PlatformSelectionError
	if errors.As(err, &selection) {
		c.Code, c.Hint = api.CodeImageManifestInvalid, selection.Error()
		c.Fix = "Publish one compatible Linux/amd64 image or pin the intended platform-specific child digest."
	} else if errors.Is(err, oci.ErrImageManifestInvalid) {
		c.Code, c.Hint = api.CodeImageManifestInvalid, "The image metadata does not meet the deployment manifest contract."
		c.Fix = "Publish a valid Linux/amd64 image or index containing one compatible image; rebuild malformed images."
	} else if errors.Is(err, context.DeadlineExceeded) {
		c.Code, c.Hint = "image_inspection_timeout", "Registry metadata inspection timed out."
	}
	return c
}

func doctorImagePlatformCheck(cfg oci.ImageConfig) doctorCheck {
	if !cfg.SupportsProductionPlatform() {
		return doctorCheck{Name: "platform", Status: "error", Code: api.CodeAppArchMismatch, Hint: "Image must declare os=linux and architecture=amd64 for production Gregale.", Fix: "Build for linux/amd64 and inspect that platform's image digest."}
	}
	return doctorCheck{Name: "platform", Status: "ok"}
}

func doctorImageContractCheck(cfg oci.ImageConfig) doctorCheck {
	_, err := oci.ManifestFromConfig(oci.Config{Entrypoint: cfg.Entrypoint, Cmd: cfg.Cmd, WorkingDir: cfg.WorkingDir, User: cfg.User, ExposedPorts: cfg.ExposedPorts, Healthcheck: cfg.Healthcheck, StopSignal: cfg.StopSignal, StopGracePeriodS: cfg.StopGracePeriodS})
	if err != nil {
		return doctorCheck{Name: "runtime-contract", Status: "error", Code: api.CodeImageManifestInvalid, Hint: err.Error(), Fix: "Provide valid ENTRYPOINT/CMD, exposed listeners, and lifecycle metadata. This check evaluates the image before deployment overrides."}
	}
	return doctorCheck{Name: "runtime-contract", Status: "ok"}
}

// Listener inference is metadata-only and precedes deployment overrides. Keep
// port selection shared with imaged rather than guessing from ENV or argv.
func doctorImageListenerCheck(cfg oci.ImageConfig) doctorCheck {
	ports, err := oci.WorkloadPortsFromExposed(cfg.ExposedPorts)
	if err != nil {
		return doctorCheck{Name: "listener", Status: "error", Code: api.CodeImageManifestInvalid, Hint: err.Error(), Fix: "Reduce exposed listeners to the supported workload port limit."}
	}
	tcp := 0
	for _, port := range ports {
		if port.Protocol == api.WorkloadPortTCP {
			tcp++
		}
	}
	if tcp > 1 {
		return doctorCheck{Name: "listener", Status: "warn", Hint: fmt.Sprintf("Multiple TCP ports are declared; the default serving port remains %d.", api.DefaultAppPort), Fix: "Set an explicit deployment port for the main HTTP listener. Additional declared TCP listeners use named routes."}
	}
	if tcp == 0 && len(ports) > 0 {
		return doctorCheck{Name: "listener", Status: "warn", Hint: "Only UDP listeners are declared; declarations alone do not create public endpoints or HTTP readiness.", Fix: "Configure and enable app-owned UDP listeners on an edge with UDP ingress and source CIDRs enabled. Request or service mode still needs its main TCP listener; non-HTTP workloads should use worker or job mode."}
	}
	port := api.DefaultAppPort
	if inferred, ok := oci.SingleTCPExposedPort(cfg.ExposedPorts); ok {
		port = inferred
	}
	return doctorCheck{Name: "listener", Status: "ok", Hint: fmt.Sprintf("Serving port %d before deployment overrides; bind the HTTP server to 0.0.0.0. Runtime reachability is not checked.", port)}
}

func doctorImageHealthcheck(hc *oci.ImageHealthcheck) doctorCheck {
	if hc == nil || len(hc.Test) == 0 || hc.Test[0] == "NONE" {
		return doctorCheck{Name: "healthcheck", Status: "skipped", Reason: "No enabled image HEALTHCHECK; application readiness still needs runtime verification."}
	}
	if hc.ImageTiming != nil {
		if err := hc.ImageTiming.Validate(); err != nil {
			return doctorCheck{Name: "healthcheck", Status: "warn", Hint: err.Error(), Fix: "Use zero for runtime defaults or durations of at least one millisecond."}
		}
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
	m.InputReference, m.SourceReference = result.InputReference, result.SourceReference
	if hc := cfg.Healthcheck; hc != nil {
		m.Healthcheck = &api.AppManifestHealthcheck{Test: hc.Test, IntervalS: hc.IntervalS, TimeoutS: hc.TimeoutS, Retries: hc.Retries, StartPeriodS: hc.StartPeriodS, ImageTiming: hc.ImageTiming}
	}
	// Report the user imaged actually starts the process as. ADR-1239 runs an
	// image without USER as root, like Docker; the manifest default ("app") is
	// for platform-built runtimes, so showing it here told the customer the
	// opposite of what runs (H10-58).
	m.User = oci.ImageProcessUser(cfg.User)
	if manifest, err := oci.ManifestFromConfig(oci.Config{Entrypoint: cfg.Entrypoint, Cmd: cfg.Cmd, User: cfg.User, ExposedPorts: cfg.ExposedPorts}); err == nil {
		m.ServingPort = manifest.EffectivePort()
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
	for path := range cfg.Volumes {
		m.VolumePaths = append(m.VolumePaths, path)
	}
	sort.Strings(m.VolumePaths)
	return m
}

func renderDoctorImage(w io.Writer, img *doctorImage) {
	_, _ = fmt.Fprintf(w, "gregale doctor — image %q\n", img.Reference)
	if img.Digest == "" {
		if img.Booted {
			_, _ = fmt.Fprintf(w, "  booted with local Docker as linux/amd64, PORT=%d (registry metadata unavailable)\n", api.DefaultAppPort)
		}
		return
	}
	if note := doctorLocalImageSourceNote(img); note != "" {
		_, _ = fmt.Fprintln(w, note)
	}
	if img.InputReference != "" {
		_, _ = fmt.Fprintf(w, "  requested: %q\n  immutable source: %q\n", img.InputReference, img.SourceReference)
	}
	_, _ = fmt.Fprintf(w, "  platform: %q\n  entrypoint: %q\n  command: %q\n  effective argv: %q\n  user: %q  working directory: %q\n  stop signal: %q\n  declared ports: %q\n", img.OS+"/"+img.Architecture, img.Entrypoint, img.Command, img.EffectiveArgv, img.User, img.WorkingDir, img.StopSignal, img.ExposedPorts)
	if img.ServingPort != 0 {
		_, _ = fmt.Fprintf(w, "  serving port: %d (before deployment overrides)\n", img.ServingPort)
	}
	if len(img.VolumePaths) > 0 {
		_, _ = fmt.Fprintf(w, "  declared volumes: %q (ephemeral; durable storage is not provisioned)\n", img.VolumePaths)
	}
	if img.Healthcheck != nil {
		_, _ = fmt.Fprintf(w, "  healthcheck: %q\n", img.Healthcheck.Test)
		hc := img.Healthcheck
		if timing := hc.ImageTiming; timing != nil {
			_, _ = fmt.Fprintf(w, "    interval: %s  timeout: %s  startup grace: %s  startup interval: %s  retries: %s\n",
				doctorImageDuration(timing.IntervalNS), doctorImageDuration(timing.TimeoutNS), time.Duration(timing.StartPeriodNS), doctorImageDuration(timing.StartIntervalNS), doctorImageRetries(hc.Retries))
		} else {
			_, _ = fmt.Fprintf(w, "    interval: %s  timeout: %s  startup grace: %s  retries: %s\n",
				doctorImageDuration(int64(hc.IntervalS)*int64(time.Second)), doctorImageDuration(int64(hc.TimeoutS)*int64(time.Second)), time.Duration(hc.StartPeriodS)*time.Second, doctorImageRetries(hc.Retries))
		}

	}
	if img.Booted {
		port := img.ServingPort
		if port == 0 {
			port = api.DefaultAppPort
		}
		_, _ = fmt.Fprintf(w, "Metadata checks, then a boot with local Docker as linux/amd64 with PORT=%d. Values precede deployment overrides.\n", port)
		return
	}
	_, _ = fmt.Fprintln(w, "Metadata checks only; no layers downloaded or containers executed. Values precede deployment overrides.")
}

func doctorImageDuration(ns int64) string {
	if ns == 0 {
		return "runtime default"
	}
	return time.Duration(ns).String()
}

func doctorImageRetries(retries int) string {
	if retries == 0 {
		return "runtime default"
	}
	return fmt.Sprint(retries)
}
