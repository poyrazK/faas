package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

// dockerHubServerURL is the key `docker login` stores Docker Hub credentials
// under, and the server URL its credential helpers expect.
const dockerHubServerURL = "https://index.docker.io/v1/"

// dockerCredentialHelperTimeout bounds one `docker-credential-<name> get`;
// some helpers ask the user to unlock a keychain.
const dockerCredentialHelperTimeout = time.Minute

// A helper name becomes part of a program name, so it is one plain word.
var dockerCredentialHelperName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// dockerCredentialHelper runs `docker-credential-<name> get` for serverURL
// and returns the username and secret it prints. found is false when the
// helper holds nothing for the server.
type dockerCredentialHelper func(ctx context.Context, name, serverURL string) (username, secret string, found bool, err error)

// dockerConfigFile is the part of Docker's config.json that holds registry
// credentials, as `docker login` writes it.
type dockerConfigFile struct {
	Auths map[string]struct {
		Auth          string `json:"auth"`
		Username      string `json:"username"`
		Password      string `json:"password"`
		IdentityToken string `json:"identitytoken"`
		RegistryToken string `json:"registrytoken"`
	} `json:"auths"`
	CredsStore  string            `json:"credsStore"`
	CredHelpers map[string]string `json:"credHelpers"`
}

// dockerConfigRegistryAuth reads the credentials a Docker config holds for
// image's registry (ADR-20261025110000): the registry's inline `auths`
// entry, else its `credHelpers` helper, else the `credsStore` helper, the
// order Docker uses. configPath is a config.json or the directory holding
// it; a leading ~/ is the home directory. Identity and registry tokens are
// refused, because a run sends a username and password to the registry.
func dockerConfigRegistryAuth(ctx context.Context, configPath, image string, helper dockerCredentialHelper) (*api.ImageRunRegistryAuth, error) {
	file, err := dockerConfigPath(configPath)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read docker config: %w", err)
	}
	var config dockerConfigFile
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("parse docker config %s: %w", file, err)
	}
	ref, err := oci.ParseReference(image)
	if err != nil {
		return nil, fmt.Errorf("image %q: %w", image, err)
	}
	registry := ref.Registry
	for key, entry := range config.Auths {
		if dockerConfigRegistry(key) != registry {
			continue
		}
		username, password := entry.Username, entry.Password
		if entry.Auth != "" {
			decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
			if err != nil {
				return nil, fmt.Errorf("docker config %s: %s auth is not base64", file, key)
			}
			var ok bool
			if username, password, ok = strings.Cut(string(decoded), ":"); !ok {
				return nil, fmt.Errorf("docker config %s: %s auth is not username:password", file, key)
			}
		}
		if username != "" && password != "" {
			return dockerRegistryAuth(username, password, registry)
		}
		if entry.IdentityToken != "" || entry.RegistryToken != "" {
			return nil, dockerTokenUnsupported(registry)
		}
	}
	name := config.CredsStore
	for key, helperName := range config.CredHelpers {
		if dockerConfigRegistry(key) == registry {
			name = helperName
		}
	}
	if name != "" {
		if !dockerCredentialHelperName.MatchString(name) {
			return nil, fmt.Errorf("docker config %s: credential helper %q is not a valid name", file, name)
		}
		serverURL := registry
		if registry == "docker.io" {
			serverURL = dockerHubServerURL
		}
		username, secret, found, err := helper(ctx, name, serverURL)
		if err != nil {
			return nil, fmt.Errorf("docker-credential-%s: %w", name, err)
		}
		if found {
			if username == "<token>" {
				return nil, dockerTokenUnsupported(registry)
			}
			return dockerRegistryAuth(username, secret, registry)
		}
	}
	return nil, fmt.Errorf("docker config %s has no credentials for %s; run `docker login %s` or use --registry-username", file, registry, registry)
}

func dockerRegistryAuth(username, password, registry string) (*api.ImageRunRegistryAuth, error) {
	if username == "" || password == "" {
		return nil, fmt.Errorf("credentials for %s in the docker config need a username and a password", registry)
	}
	if len(password) > api.MaxRegistryPasswordBytes {
		return nil, fmt.Errorf("registry password exceeds %d bytes", api.MaxRegistryPasswordBytes)
	}
	return &api.ImageRunRegistryAuth{Username: username, Password: password}, nil
}

func dockerTokenUnsupported(registry string) error {
	return fmt.Errorf("the docker config holds an identity token for %s, which image runs cannot use; use --registry-username with a password or access token", registry)
}

// dockerConfigPath returns the config.json a --docker-config value names.
func dockerConfigPath(configPath string) (string, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "~" || strings.HasPrefix(configPath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve ~ in --docker-config: %w", err)
		}
		configPath = filepath.Join(home, strings.TrimPrefix(configPath, "~"))
	}
	info, err := os.Stat(configPath)
	if err != nil {
		return "", fmt.Errorf("read docker config: %w", err)
	}
	if info.IsDir() {
		return filepath.Join(configPath, "config.json"), nil
	}
	return configPath, nil
}

// dockerConfigRegistry reduces an `auths` or `credHelpers` key to the
// canonical registry name image references use: no scheme or path, and
// Docker Hub's legacy hosts as docker.io.
func dockerConfigRegistry(key string) string {
	host := strings.ToLower(strings.TrimSpace(key))
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	host, _, _ = strings.Cut(host, "/")
	switch host {
	case "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com":
		return "docker.io"
	}
	return host
}

// runDockerCredentialHelper runs a credential helper the way Docker does:
// `docker-credential-<name> get` with the server URL on stdin and a JSON
// {Username, Secret} on stdout.
func runDockerCredentialHelper(ctx context.Context, name, serverURL string) (string, string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, dockerCredentialHelperTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker-credential-"+name, "get")
	cmd.Stdin = strings.NewReader(serverURL)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stdout.String()+stderr.String(), "credentials not found") {
			return "", "", false, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", "", false, fmt.Errorf("exited with status %d", exitErr.ExitCode())
		}
		return "", "", false, err
	}
	var creds struct {
		Username string `json:"Username"`
		Secret   string `json:"Secret"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &creds); err != nil {
		return "", "", false, errors.New("printed something other than credentials")
	}
	return creds.Username, creds.Secret, true, nil
}
