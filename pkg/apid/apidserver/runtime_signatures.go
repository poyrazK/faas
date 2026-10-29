package apidserver

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/sealsig"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// envRuntimeSigningKey names apid's runtime secret signing key (ADR
// 20261028161502, amendment 1). Unset, apid seals runtime secrets unsigned.
const envRuntimeSigningKey = "FAAS_APID_RUNTIME_SIGNING_KEY_FILE"

// installRuntimeSigner makes every runtime secret this process seals carry
// a writer signature, stored before the seal returns, when the signing key
// is configured. The returned func removes the hook.
func installRuntimeSigner(getenv func(string) string, store any, log *slog.Logger) (func(), error) {
	path := getenv(envRuntimeSigningKey)
	if path == "" {
		return func() {}, nil
	}
	key, err := sealsig.LoadSigningKey(path)
	if err != nil {
		return nil, fmt.Errorf("apid: runtime secret signing key: %w", err)
	}
	signatures, ok := store.(sealsig.RuntimeStore)
	if !ok {
		return nil, errors.New("apid: runtime secret signing needs a store that keeps signatures")
	}
	secretbox.SetRuntimeSealHook(sealsig.RuntimeSealHook(signatures, sealsig.NewSigner(key)))
	log.Info("runtime secret signing enabled")
	return func() { secretbox.SetRuntimeSealHook(nil) }, nil
}
