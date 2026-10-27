package api

import (
	"encoding/base64"
	"encoding/json"
	"sort"
)

// BuildRegistryAuthKey is the secrets.env entry that carries a builder VM's
// Docker config (ADR-20261026090000). vmmd writes it after the build's own
// secrets, so it replaces any app secret of the same name; guest-init moves
// it into a DOCKER_CONFIG directory for buildctl and never mounts it as a
// build secret.
const BuildRegistryAuthKey = "GREGALE_BUILD_REGISTRY_AUTH"

// dockerHubAuthKey is where Docker clients and BuildKit look up Docker Hub
// credentials in a Docker config.
const dockerHubAuthKey = "https://index.docker.io/v1/"

// RegistryLogin is one registry's username and password.
type RegistryLogin struct {
	Registry string
	Username string
	Password string
}

// DockerConfigJSON renders logins as a Docker config.json. Docker Hub, stored
// as registry-1.docker.io or docker.io, is keyed by its legacy index URL.
func DockerConfigJSON(logins []RegistryLogin) ([]byte, error) {
	type auth struct {
		Auth string `json:"auth"`
	}
	auths := make(map[string]auth, len(logins))
	sorted := append([]RegistryLogin(nil), logins...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Registry < sorted[j].Registry })
	for _, login := range sorted {
		key := login.Registry
		switch key {
		case "docker.io", "registry-1.docker.io", "index.docker.io":
			key = dockerHubAuthKey
		}
		auths[key] = auth{Auth: base64.StdEncoding.EncodeToString([]byte(login.Username + ":" + login.Password))}
	}
	return json.Marshal(struct {
		Auths map[string]auth `json:"auths"`
	}{auths})
}
