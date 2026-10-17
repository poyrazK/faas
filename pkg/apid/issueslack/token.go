package issueslack

import (
	"errors"
	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func OpenToken(identities []*age.X25519Identity, blob []byte) (string, error) {
	namespace, raw, err := secretbox.OpenBytesMulti(identities, blob)
	if err != nil || namespace != state.IssueSlackTokenNamespace || !ValidBotToken(string(raw)) {
		return "", errors.New("slack token unavailable")
	}
	return string(raw), nil
}
