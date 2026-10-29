package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

// buildSecretEnvName is the buildctl environment variable that carries build
// secret i (ADR-20261022185329); buildArgv names it in --secret.
func buildSecretEnvName(i int) string {
	return fmt.Sprintf("GREGALE_BUILD_SECRET_%d", i)
}

// buildDockerConfigDir holds the Docker config a Dockerfile build pulls its
// base images with (ADR-20261026091500). It is on the guest's RAM-backed
// /tmp, so the credentials never reach the exported build drive.
const buildDockerConfigDir = "/tmp/gregale-build-docker"

// buildSecretEnv reads what vmmd delivered in secrets.env for a Dockerfile
// build: its build secrets and the Docker config of the app's registry
// credentials. It deletes the file so the values do not stay on the build
// drive, and returns buildctl's environment entries. A declared secret that
// was not delivered fails the build rather than running it without the value.
func buildSecretEnv(m api.BuildManifest) ([]string, error) {
	return buildSecretEnvAt(m, secretsEnvPath, buildDockerConfigDir)
}

// buildSecretEnvAt is buildSecretEnv for the secrets file at secretsPath,
// staging the Docker config under dockerDir.
func buildSecretEnvAt(m api.BuildManifest, secretsPath, dockerDir string) ([]string, error) {
	if m.Framework != api.FrameworkDockerfile {
		return nil, nil
	}
	values, err := loadSecretsFile(secretsPath, nil)
	_ = os.Remove(secretsPath)
	if err != nil {
		return nil, fmt.Errorf("read build secrets: %w", err)
	}
	var env []string
	if config, ok := values[api.BuildRegistryAuthKey]; ok {
		delete(values, api.BuildRegistryAuthKey)
		if err := writeBuildDockerConfig(dockerDir, config); err != nil {
			return nil, fmt.Errorf("stage registry credentials: %w", err)
		}
		env = append(env, "DOCKER_CONFIG="+dockerDir)
	}
	if m.DockerfileBuild == nil || len(m.DockerfileBuild.Secrets) == 0 {
		return env, nil
	}
	secretEnv, err := buildSecretEnvFrom(m.DockerfileBuild.Secrets, values)
	if err != nil {
		return nil, err
	}
	return append(env, secretEnv...), nil
}

// writeBuildDockerConfig writes config as dir/config.json, readable by root
// alone; buildctl reads it through DOCKER_CONFIG for registry logins.
func writeBuildDockerConfig(dir, config string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o600)
}

func buildSecretEnvFrom(secrets []api.DockerfileBuildSecret, values map[string]string) ([]string, error) {
	env := make([]string, 0, len(secrets))
	for i, secret := range secrets {
		value, ok := values[secret.Secret]
		if !ok {
			return nil, fmt.Errorf("build secret %s (app secret %s) was not delivered to the builder", secret.ID, secret.Secret)
		}
		env = append(env, buildSecretEnvName(i)+"="+value)
	}
	return env, nil
}
