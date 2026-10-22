package main

import (
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// buildSecretEnvName is the buildctl environment variable that carries build
// secret i (ADR-20261022185329); buildArgv names it in --secret.
func buildSecretEnvName(i int) string {
	return fmt.Sprintf("GREGALE_BUILD_SECRET_%d", i)
}

// buildSecretEnv reads the build secrets vmmd delivered in secrets.env,
// deletes the file so the values do not stay on the build drive, and returns
// buildctl's environment entries. A declared secret that was not delivered
// fails the build rather than running it without the value.
func buildSecretEnv(m api.BuildManifest) ([]string, error) {
	if m.Framework != api.FrameworkDockerfile || m.DockerfileBuild == nil || len(m.DockerfileBuild.Secrets) == 0 {
		return nil, nil
	}
	values, err := loadSecrets(nil)
	_ = os.Remove(secretsEnvPath)
	if err != nil {
		return nil, fmt.Errorf("read build secrets: %w", err)
	}
	return buildSecretEnvFrom(m.DockerfileBuild.Secrets, values)
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
