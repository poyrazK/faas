//go:build linux

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// prepareJobProcess resolves the command a job runs. An image run
// (ADR-1790) takes ENV, USER and WORKDIR from the image's /etc/faas/app.json,
// which imaged writes from the OCI config, and resolves a bare command on
// the image PATH; other jobs keep the guest default. A failure is the exit
// receipt to ship instead of running the command.
func prepareJobProcess(m JobManifest, secrets map[string]string, log *slog.Logger) (jobProcess, *JobExitPayload) {
	if !m.ImageProcess {
		return guestJobProcess(m, buildEnvForJobWithSecrets(m, secrets)), nil
	}
	fail := func(code int32, msg string, err error) (jobProcess, *JobExitPayload) {
		log.Error("runJob: "+msg, "err", err)
		return jobProcess{}, &JobExitPayload{ExitCode: code, ErrorClass: "infra", LeaseToken: m.LeaseToken,
			FinishedAtUnixNano: time.Now().UnixNano()}
	}
	app, err := loadImageJobManifest()
	if err != nil {
		return fail(126, "load image process config", err)
	}
	credential, err := processCredential("", imageJobUser(m, app))
	if err != nil {
		return fail(126, "resolve image user", err)
	}
	env := buildImageJobEnv(os.Environ(), app.Env, m, secrets)
	env = withImageHome(env, imagePasswdHome(credential.Uid))
	dir := imageJobWorkdir(m, app)
	if err := ensureJobWorkdir(dir, credential.Uid, credential.Gid); err != nil {
		return fail(126, "prepare working directory", err)
	}
	argv := append([]string(nil), m.Command...)
	argv[0] = resolveWorkloadCommandPath("/", argv[0], env)
	return jobProcess{Argv: argv, Env: env, Dir: dir, Credential: execProcessCredential(credential)}, nil
}

func loadImageJobManifest() (api.AppManifest, error) {
	//nolint:forbidigo // platform-owned manifest that imaged staged in the job image artifact
	f, err := os.Open(api.AppManifestPath)
	if err != nil {
		return api.AppManifest{}, fmt.Errorf("open %s: %w", api.AppManifestPath, err)
	}
	defer func() { _ = f.Close() }()
	return api.ReadManifest(f)
}

// imagePasswdHome reads the image's passwd for uid's home directory.
func imagePasswdHome(uid uint32) string {
	root, err := os.OpenRoot("/")
	if err != nil {
		return "/"
	}
	defer func() { _ = root.Close() }()
	passwd, err := fs.ReadFile(root.FS(), "etc/passwd")
	if err != nil {
		return "/"
	}
	return passwdHome(string(passwd), uid)
}

// ensureJobWorkdir creates a missing working directory owned by the
// process identity, as `docker run -w` creates one.
func ensureJobWorkdir(dir string, uid, gid uint32) error {
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("working directory %s is not a directory", dir)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.Chown(dir, int(uid), int(gid))
}
