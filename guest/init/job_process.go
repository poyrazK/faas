package main

// Build-tag-free half of a job's resolved process (ADR-1790): an image run
// executes with the image's ENV, USER and WORKDIR, as `docker run` does,
// and -u/-w override USER and WORKDIR. Resolution against the image root
// lives in job_process_linux.go.

import (
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
)

// defaultImagePath is Docker's PATH for an image that sets none.
const defaultImagePath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// jobProcess is the command a job task runs. Credential nil keeps PID 1's
// root identity; Dir empty keeps the guest root.
type jobProcess struct {
	Argv       []string
	Env        []string
	Dir        string
	Credential *syscall.Credential
}

// guestJobProcess is the process of an ordinary job: its command with the
// guest environment, unchanged by ADR-1790.
func guestJobProcess(m JobManifest, env []string) jobProcess {
	return jobProcess{Argv: append([]string(nil), m.Command...), Env: env}
}

// buildImageJobEnv layers the job's environment over the image ENV, as
// `docker run -e` layers over the image: base (the guest's own environment)
// < image ENV < job env < secrets < the platform job baseline. An image
// without PATH gets Docker's default.
func buildImageJobEnv(base []string, image map[string]string, m JobManifest, secrets map[string]string) []string {
	merged := make(map[string]string, len(base)+len(image)+len(m.Env)+len(secrets)+len(jobEnvBaseline))
	for _, kv := range base {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			merged[k] = v
		}
	}
	for _, layer := range []map[string]string{image, m.Env, secrets, jobEnvBaseline} {
		for k, v := range layer {
			if validEnvKey(k) {
				merged[k] = v
			}
		}
	}
	if merged["PATH"] == "" {
		merged["PATH"] = defaultImagePath
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// imageJobUser is the identity spec an image run starts with: the -u
// override, else the image USER, else root. A customer image without USER
// runs as root, as under Docker (ADR-1239).
func imageJobUser(m JobManifest, app api.AppManifest) string {
	if m.User != "" {
		return m.User
	}
	if app.User != "" {
		return app.User
	}
	return "0"
}

// imageJobWorkdir is the -w override, else the image WORKDIR, else "/".
func imageJobWorkdir(m JobManifest, app api.AppManifest) string {
	if m.Workdir != "" {
		return m.Workdir
	}
	return app.EffectiveWorkingDir()
}

// passwdHome returns uid's home directory from passwd contents, or "/" as
// Docker uses for a user without a passwd entry.
func passwdHome(passwd string, uid uint32) string {
	want := strconv.FormatUint(uint64(uid), 10)
	for _, line := range strings.Split(passwd, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) >= 6 && fields[2] == want && strings.HasPrefix(fields[5], "/") {
			return fields[5]
		}
	}
	return "/"
}

// withImageHome sets HOME from the user's passwd entry when neither the
// image nor the job set it, as Docker does.
func withImageHome(env []string, home string) []string {
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") && kv != "HOME=" {
			return env
		}
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if kv != "HOME=" {
			out = append(out, kv)
		}
	}
	return append(out, "HOME="+home)
}
