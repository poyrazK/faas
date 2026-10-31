package fcvm

import (
	"errors"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// registryCredentialNamespace is the secretbox namespace apid seals
// app_registry_credentials passwords under (ADR-062).
const registryCredentialNamespace = "registry_creds"

var errRegistryNamespace = errors.New("registry credential sealed under another namespace")

// BuildRegistryCredential is one sealed registry credential of the app a
// builder VM builds (ADR-20261026091500). builderd forwards the row; only
// the Manager opens the password.
type BuildRegistryCredential struct {
	Registry       string
	Username       string
	PasswordSealed []byte
}

// SetRegistryIdentities attaches the identities that open registry
// passwords: the build seal domain first, then the fleet and host
// identities for passwords sealed before the domain (ADR 20261022111812).
// NOT safe to call concurrently with Wake; production wires it before
// serving traffic.
func (m *Manager) SetRegistryIdentities(ids []*age.X25519Identity) {
	m.registryIdentities = ids
}

// buildRegistryAuth opens a builder's registry credentials into the Docker
// config its buildctl reads. A credential that does not open is left out
// and logged: the build then pulls that registry anonymously, as builds did
// before, instead of failing a build that may not need it.
func (m *Manager) buildRegistryAuth(creds []BuildRegistryCredential) (string, bool) {
	if len(creds) == 0 {
		return "", false
	}
	logins := make([]api.RegistryLogin, 0, len(creds))
	for _, cred := range creds {
		password, err := m.openRegistryPassword(cred.PasswordSealed)
		if err != nil {
			m.log.Warn("fcvm: builder registry credential not opened; the build pulls that registry anonymously",
				"registry", logsanitize.Field(cred.Registry), "err", err)
			continue
		}
		logins = append(logins, api.RegistryLogin{Registry: cred.Registry, Username: cred.Username, Password: password})
	}
	if len(logins) == 0 {
		return "", false
	}
	config, err := api.DockerConfigJSON(logins)
	if err != nil {
		m.log.Warn("fcvm: builder registry config not rendered", "err", err)
		return "", false
	}
	return string(config), true
}

func (m *Manager) openRegistryPassword(sealed []byte) (string, error) {
	if len(m.registryIdentities) == 0 {
		return "", ErrNoHostKey
	}
	ns, plaintext, err := secretbox.OpenBytesMulti(m.registryIdentities, sealed)
	if err != nil {
		return "", err
	}
	if ns != registryCredentialNamespace {
		return "", errRegistryNamespace
	}
	return string(plaintext), nil
}
