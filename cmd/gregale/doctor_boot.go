package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale doctor --image REF --boot` runs the image with the local Docker
// client, as the platform would start it (linux/amd64, PORT set to the serving
// port), and checks the container contract metadata cannot: that the process
// starts, answers HTTP on 0.0.0.0:$PORT and keeps running. It costs nothing on
// the platform and needs no account. The container is always removed.

// Boot timings are variables so tests can shorten them.
var (
	doctorBootStartupTimeout = 30 * time.Second
	// doctorBootStayUp is Gregale's observation window for a new deployment.
	doctorBootStayUp = api.CharacterizationDeadline
	doctorBootPoll   = 250 * time.Millisecond
	// doctorBootRunTimeout bounds `docker run`, which pulls a missing image.
	doctorBootRunTimeout = 10 * time.Minute
)

type doctorBootOptions struct {
	image   string
	port    int
	envFile string
	// memoryMB caps the container's memory, swap included, at the RAM the
	// app would get on Gregale; memorySource says where the number came from.
	memoryMB     int
	memorySource string
	// healthPath is probed instead of / and must answer 2xx, as Gregale's
	// startup readiness requires; probeHost is the app's public host, sent
	// with X-Forwarded-Proto as that probe sends them.
	healthPath string
	probeHost  string
	// imageHealthcheck is the image's enabled HEALTHCHECK, which Docker runs
	// (doctor_boot_health.go); nil when it has none.
	imageHealthcheck *api.AppManifestHealthcheck
	// deploy is set under `deploy --local-image --boot`, whose fixes name
	// deploy's flags instead of doctor's.
	deploy bool
	// mode is the app's execution mode; a worker or job is judged without
	// HTTP (doctor_boot_mode.go).
	mode string
	// grpcHealth probes grpc.health.v1 for grpcService instead of HTTP
	// (doctor_boot_grpc.go).
	grpcHealth  bool
	grpcService string
}

// protocol is what the boot expects the app to serve on $PORT.
func (o doctorBootOptions) protocol() string {
	if o.grpcHealth {
		return "plaintext gRPC"
	}
	return "HTTP"
}

// envFileFlag is the flag that passes env vars to the boot container.
func (o doctorBootOptions) envFileFlag() string {
	if o.deploy {
		return "--boot-env-file"
	}
	return "--env-file"
}

// doctorDocker runs the local Docker client. GREGALE_DOCKER selects a
// compatible client such as podman, as for --local-image.
type doctorDocker struct{ bin string }

func newDoctorDocker() (doctorDocker, error) {
	bin := strings.TrimSpace(os.Getenv("GREGALE_DOCKER"))
	if bin == "" {
		bin = "docker"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return doctorDocker{}, fmt.Errorf("%s was not found", bin)
	}
	return doctorDocker{bin: bin}, nil
}

func (d doctorDocker) run(ctx context.Context, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, d.bin, args...) //nolint:gosec // argv only, no shell; the binary is the customer's Docker client.
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return stdout.String(), fmt.Errorf("%s %s: %s", d.bin, args[0], truncateBootText(detail, 500))
	}
	return stdout.String(), nil
}

type doctorBootState struct {
	running   bool
	exitCode  int
	oomKilled bool
}

func (d doctorDocker) state(ctx context.Context, name string) (doctorBootState, error) {
	out, err := d.run(ctx, "inspect", "--format", "{{.State.Running}} {{.State.ExitCode}} {{.State.OOMKilled}}", name)
	if err != nil {
		return doctorBootState{}, err
	}
	fields := strings.Fields(out)
	if len(fields) != 3 {
		return doctorBootState{}, fmt.Errorf("%s inspect: unexpected output %q", d.bin, truncateBootText(out, 100))
	}
	code, _ := strconv.Atoi(fields[1])
	return doctorBootState{running: fields[0] == "true", exitCode: code, oomKilled: fields[2] == "true"}, nil
}

// logTail returns the container's last output lines for a crash report.
func (d doctorDocker) logTail(ctx context.Context, name string) string {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, d.bin, "logs", "--tail", "20", name) //nolint:gosec // argv only.
	cmd.Stdout, cmd.Stderr = &out, &out
	_ = cmd.Run()
	return truncateBootText(strings.TrimSpace(out.String()), 2000)
}

func (d doctorDocker) remove(ctx context.Context, name string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_, _ = d.run(ctx, "rm", "--force", "--volumes", name)
}

// runDoctorBootChecks returns the boot-* checks. A failed step skips the ones
// after it.
func runDoctorBootChecks(ctx context.Context, opts doctorBootOptions) []doctorCheck {
	docker, err := newDoctorDocker()
	if err != nil {
		return []doctorCheck{{Name: "boot-start", Status: "error", Code: "boot_docker_missing", Hint: err.Error() + "; --boot runs the image with your local Docker client.", Fix: "Install and start Docker (or set GREGALE_DOCKER to a compatible client such as podman), or drop --boot."}}
	}
	name := doctorBootContainerName()
	portSpec := strconv.Itoa(opts.port)
	args := []string{"run", "--detach", "--name", name, "--platform", "linux/amd64", "--label", "dev.gregale.doctor=boot",
		"--env", "PORT=" + portSpec}
	if !doctorBootNoListener(opts.mode) {
		args = append(args, "--publish", "127.0.0.1::"+portSpec)
	}
	if opts.memoryMB > 0 {
		// Without --memory-swap Docker allows as much swap again, and an app
		// over its RAM would slow down instead of being killed as on Gregale.
		limit := strconv.Itoa(opts.memoryMB) + "m"
		args = append(args, "--memory", limit, "--memory-swap", limit)
	}
	if opts.imageHealthcheck != nil {
		args = append(args, doctorBootHealthArgs(opts.imageHealthcheck)...)
	}
	if opts.envFile != "" {
		args = append(args, "--env-file", opts.envFile)
	}
	args = append(args, opts.image)
	if doctorBootNoListener(opts.mode) {
		doctorBootRealRunNote(opts.mode)
	}
	_, _ = fmt.Fprintf(osStderr, "Starting %s with %s (pulls the image if it is not local)…\n", opts.image, docker.bin)
	runCtx, cancel := context.WithTimeout(ctx, doctorBootRunTimeout)
	_, err = docker.run(runCtx, args...)
	cancel()
	// A failed run can still leave a created container behind.
	defer docker.remove(ctx, name)
	if err != nil {
		return []doctorCheck{{Name: "boot-start", Status: "error", Code: "boot_run_failed", Hint: "Could not start the image: " + err.Error(), Fix: "Check that Docker is running and the image can be pulled (`docker pull --platform linux/amd64 " + opts.image + "`)."}}
	}
	started := time.Now()
	b := doctorBoot{docker: docker, name: name, opts: opts, started: started}
	return b.check(ctx)
}

type doctorBoot struct {
	docker  doctorDocker
	name    string
	opts    doctorBootOptions
	started time.Time
}

func (b doctorBoot) check(ctx context.Context) []doctorCheck {
	if doctorBootNoListener(b.opts.mode) {
		return b.checkNoListener(ctx)
	}
	if b.opts.grpcHealth {
		return b.checkGRPC(ctx)
	}
	status, exited, err := b.waitForHTTP(ctx)
	if err != nil {
		return []doctorCheck{{Name: "boot-start", Status: "error", Code: "boot_inspect_failed", Hint: err.Error()}}
	}
	if exited != nil {
		return []doctorCheck{b.exitedCheck(ctx, "boot-start", *exited)}
	}
	if status == 0 {
		return []doctorCheck{
			{Name: "boot-start", Status: "ok", Detail: "container running" + b.memoryNote()},
			b.listenerProblem(ctx),
		}
	}
	checks := []doctorCheck{
		{Name: "boot-start", Status: "ok", Detail: fmt.Sprintf("answered in %s", bootSeconds(time.Since(b.started))) + b.memoryNote()},
		{Name: "boot-listener", Status: "ok", Detail: fmt.Sprintf("listening on 0.0.0.0:%d ($PORT)", b.opts.port)},
		b.httpCheck(status),
	}
	if b.opts.imageHealthcheck != nil {
		checks = append(checks, b.imageHealth(ctx))
	}
	return append(checks, b.stayUp(ctx))
}

// probePath is the path the boot probes: the health path when one is set,
// as Gregale's readiness does, otherwise /.
func (b doctorBoot) probePath() string {
	if b.opts.healthPath != "" {
		return b.opts.healthPath
	}
	return "/"
}

// doctorBootHealthy is Gregale's startup readiness rule for a health path
// (pkg/fcvm healthcheckProbe): only 2xx passes, after redirects.
func doctorBootHealthy(status int) bool {
	return status/100 == 2
}

func (b doctorBoot) httpCheck(status int) doctorCheck {
	detail := fmt.Sprintf("GET %s → %d", b.probePath(), status)
	if b.opts.healthPath != "" {
		if doctorBootHealthy(status) {
			return doctorCheck{Name: "boot-http", Status: "ok", Detail: detail}
		}
		if status == http.StatusBadRequest && b.opts.probeHost == "" {
			// Gregale's probe sends the app's public host; this one could not.
			fix := "Pass --app SLUG so the probe sends that app's public host, as Gregale does."
			if b.opts.deploy {
				fix = "The app has no public host yet, so the local probe could not send one; Gregale's probe will. Allow 127.0.0.1 for the local boot with " + b.opts.envFileFlag() + ", or deploy without --boot."
			}
			return doctorCheck{Name: "boot-http", Status: "warn", Detail: detail,
				Hint: fmt.Sprintf("GET %s answered 400 to a request for host %s. An app that allow-lists its host names (Django ALLOWED_HOSTS, Rails config.hosts) refuses it, but Gregale's probe sends the app's public host.", b.opts.healthPath, "127.0.0.1"),
				Fix:  fix}
		}
		return doctorCheck{Name: "boot-http", Status: "error", Code: "boot_health_failed", Detail: detail,
			Hint: fmt.Sprintf("GET %s last answered %d, and no answer within %s was 2xx. Gregale marks an instance ready only once its health path answers 2xx, so the deploy would fail.", b.opts.healthPath, status, bootSeconds(doctorBootStartupTimeout)),
			Fix:  "Make the health path answer 2xx once the app can serve (a redirect counts only if it ends in 2xx), check that the path is right, and set env vars the app needs with " + b.opts.envFileFlag() + "."}
	}
	if status >= 500 {
		return doctorCheck{Name: "boot-http", Status: "warn", Detail: detail, Hint: fmt.Sprintf("GET / answered %d. The platform marks an instance ready once the port accepts connections (or its health check passes), but every request may fail the same way.", status), Fix: "Check the container logs (`docker logs`) for the error; set env vars the app needs with " + b.opts.envFileFlag() + ", or pass --healthcheck-path to probe the app's health route."}
	}
	return doctorCheck{Name: "boot-http", Status: "ok", Detail: detail}
}

// waitForHTTP polls the probe path until it answers (with a health path: a
// passing status), the container exits or the startup timeout passes. It
// returns the last status, 0 when nothing answered.
func (b doctorBoot) waitForHTTP(ctx context.Context) (int, *doctorBootState, error) {
	client := b.probeClient()
	deadline := time.Now().Add(doctorBootStartupTimeout)
	hostPort := ""
	last := 0
	for {
		st, err := b.docker.state(ctx, b.name)
		if err != nil {
			return 0, nil, err
		}
		if !st.running {
			return 0, &st, nil
		}
		if hostPort == "" {
			hostPort = b.publishedAddr(ctx)
		}
		if hostPort != "" {
			if status := doctorBootGet(ctx, client, hostPort, b.probePath(), b.opts.probeHost); status != 0 {
				last = status
				if b.opts.healthPath == "" || doctorBootHealthy(status) {
					return status, nil, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return last, nil, nil
		}
		select {
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		case <-time.After(doctorBootPoll):
		}
	}
}

// publishedAddr is the host address Docker published the container port on.
func (b doctorBoot) publishedAddr(ctx context.Context) string {
	out, err := b.docker.run(ctx, "port", b.name, strconv.Itoa(b.opts.port)+"/tcp")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if addr := strings.TrimSpace(line); addr != "" {
			return addr
		}
	}
	return ""
}

// probeClient follows a health path's redirects as Gregale's probe does,
// but only within the container: a redirect elsewhere (often the public
// https URL) would reach the live site from this machine, and on Gregale
// it leaves the guest network and fails, so it stops there and its 3xx
// fails the check. Without a health path any answer counts, unfollowed.
func (b doctorBoot) probeClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if b.opts.healthPath == "" || len(via) >= 10 || req.URL.Host != via[0].URL.Host {
			return http.ErrUseLastResponse
		}
		req.Host = via[0].Host
		return nil
	}}
}

// doctorBootGet makes one probe GET. A probeHost is sent as the Host with
// X-Forwarded-Proto: https, as Gregale's readiness probe sends them.
func doctorBootGet(ctx context.Context, client *http.Client, hostPort, path, probeHost string) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+hostPort+path, nil)
	if err != nil {
		return 0
	}
	if probeHost != "" {
		req.Host = probeHost
		req.Header.Set("X-Forwarded-Proto", "https")
	}
	resp, err := client.Do(req)
	if err != nil {
		// Docker's port proxy accepts and then drops connections while
		// nothing listens in the container: keep polling.
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func (b doctorBoot) stayUp(ctx context.Context) doctorCheck {
	select {
	case <-ctx.Done():
		return doctorCheck{Name: "boot-stay-up", Status: "skipped", Reason: "interrupted"}
	case <-time.After(time.Until(b.started.Add(doctorBootStayUp))):
	}
	st, err := b.docker.state(ctx, b.name)
	if err != nil {
		return doctorCheck{Name: "boot-stay-up", Status: "error", Code: "boot_inspect_failed", Hint: err.Error()}
	}
	if !st.running {
		return b.exitedCheck(ctx, "boot-stay-up", st)
	}
	return doctorCheck{Name: "boot-stay-up", Status: "ok", Detail: fmt.Sprintf("stayed up %s", bootSeconds(doctorBootStayUp))}
}

func (b doctorBoot) exitedCheck(ctx context.Context, name string, st doctorBootState) doctorCheck {
	c := doctorCheck{Name: name, Status: "error", Code: "boot_exited",
		Hint: fmt.Sprintf("The container exited with code %d after %s.", st.exitCode, bootSeconds(time.Since(b.started))),
		Fix:  "Read the container output; an app that needs env vars to start can get them with " + b.opts.envFileFlag() + ". A request-serving app must keep running and serve " + b.opts.protocol() + " on 0.0.0.0:$PORT; a one-off task belongs in `gregale run`."}
	if st.oomKilled {
		c.Code, c.Hint = "boot_oom_killed", fmt.Sprintf("The container was killed for running out of memory after %s.", bootSeconds(time.Since(b.started)))
		if b.opts.memoryMB > 0 {
			c.Hint = fmt.Sprintf("The container ran out of its %d MB of memory (%s) after %s; on Gregale the app would crash the same way.", b.opts.memoryMB, b.opts.memorySource, bootSeconds(time.Since(b.started)))
			c.Fix = "Lower the app's memory use (heap or worker settings), or give the app more RAM with `gregale app update APP --ram MB`, up to your plan's limit; check again with `--ram MB`."
			if b.opts.deploy {
				c.Fix = "Lower the app's memory use (heap or worker settings), or deploy with a larger --size, up to your plan's limit."
			}
		}
	}
	if tail := b.docker.logTail(ctx, b.name); tail != "" {
		c.Output = tail
	}
	return c
}

// listenerProblem explains why GET / got no answer by reading the
// container's listening sockets. It needs `cat` in the image; without it the
// check reports the generic contract.
func (b doctorBoot) listenerProblem(ctx context.Context) doctorCheck {
	port := b.opts.port
	c := doctorCheck{Name: "boot-listener", Status: "error", Code: "boot_not_listening",
		Hint: fmt.Sprintf("Nothing answered %s on 0.0.0.0:%d ($PORT) within %s.", b.opts.protocol(), port, bootSeconds(doctorBootStartupTimeout)),
		Fix:  "Bind 0.0.0.0 and read PORT. A worker without a listener deploys in worker mode."}
	if b.opts.grpcHealth {
		c.Fix = "Serve plaintext gRPC on 0.0.0.0 at $PORT. A worker without a listener deploys in worker mode."
	}
	out, err := b.docker.run(ctx, "exec", b.name, "cat", "/proc/net/tcp", "/proc/net/tcp6")
	if err != nil && out == "" {
		return c
	}
	listeners := parseProcNetListeners(out)
	var onPort, loopback, elsewhere []string
	for _, l := range listeners {
		switch {
		case l.port == port && !l.ip.IsLoopback():
			onPort = append(onPort, l.String())
		case l.port == port:
			loopback = append(loopback, l.String())
		default:
			elsewhere = append(elsewhere, l.String())
		}
	}
	switch {
	case len(onPort) > 0:
		c.Hint = fmt.Sprintf("The app accepts connections on %s but did not answer GET %s with HTTP within %s.", onPort[0], b.probePath(), bootSeconds(doctorBootStartupTimeout))
		c.Fix = "Serve HTTP on $PORT. A non-HTTP TCP service needs an explicit TCP route; a worker deploys in worker mode."
		if b.opts.grpcHealth {
			c.Hint = fmt.Sprintf("The app accepts connections on %s but did not answer plaintext gRPC within %s.", onPort[0], bootSeconds(doctorBootStartupTimeout))
			c.Fix = "Serve plaintext gRPC (h2c) on $PORT: Gregale's probe does not use TLS, and Gregale terminates TLS at its edge."
		}
	case len(loopback) > 0:
		c.Hint = fmt.Sprintf("Listening on %s, not 0.0.0.0:%d; the platform cannot reach a loopback listener.", loopback[0], port)
		c.Fix = "Bind 0.0.0.0 (or ::) instead of 127.0.0.1/localhost."
	case len(elsewhere) > 0:
		c.Hint = fmt.Sprintf("Listening on %s, not 0.0.0.0:%d ($PORT).", strings.Join(elsewhere, ", "), port)
		c.Fix = "Bind 0.0.0.0 and read PORT, or EXPOSE the one port the app listens on so the platform sets PORT to it."
	}
	return c
}

type procNetListener struct {
	ip   net.IP
	port int
}

func (l procNetListener) String() string {
	return net.JoinHostPort(l.ip.String(), strconv.Itoa(l.port))
}

// parseProcNetListeners reads /proc/net/tcp{,6} text and returns sockets in
// the LISTEN state (0A).
func parseProcNetListeners(text string) []procNetListener {
	var out []procNetListener
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[3] != "0A" {
			continue
		}
		addr, portHex, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(portHex, 16, 16)
		if err != nil {
			continue
		}
		ip, err := procNetIP(addr)
		if err != nil {
			continue
		}
		l := procNetListener{ip: ip, port: int(port)}
		if !seen[l.String()] {
			seen[l.String()] = true
			out = append(out, l)
		}
	}
	return out
}

// procNetIP decodes the kernel's hex address: each 32-bit word is in host
// (little-endian) byte order.
func procNetIP(s string) (net.IP, error) {
	raw, err := hex.DecodeString(s)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return nil, errors.New("bad address")
	}
	ip := make(net.IP, len(raw))
	for i := 0; i < len(raw); i += 4 {
		ip[i], ip[i+1], ip[i+2], ip[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	if v4 := ip.To4(); v4 != nil {
		return v4, nil
	}
	return ip, nil
}

// memoryNote names the memory limit on a passed start check.
func (b doctorBoot) memoryNote() string {
	if b.opts.memoryMB <= 0 {
		return ""
	}
	return fmt.Sprintf(" within %d MB (%s)", b.opts.memoryMB, b.opts.memorySource)
}

func doctorBootContainerName() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "gregale-doctor-" + hex.EncodeToString(b[:])
}

func bootSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + " s"
}

func truncateBootText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
