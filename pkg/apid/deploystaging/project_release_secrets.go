package deploystaging

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func SealProjectReleaseSecrets(account string, apps []state.App, request api.ProjectReleaseSecretStageRequest, limits api.Limits, hmacKey []byte, seal func(string, string) ([]byte, string, string, error)) ([]state.ProjectReleaseSealedSecret, string, error) {
	raw, _ := json.Marshal(request.Workloads)
	if _, err := api.DecodeProjectReleaseSecrets(bytes.NewReader(raw), limits); err != nil {
		return nil, "", err
	}
	if len(hmacKey) == 0 || !api.ValidDeploymentAttemptID(request.RequestID) {
		return nil, "", fmt.Errorf("release secret staging is unavailable")
	}
	mac := hmac.New(sha256.New, hmacKey)
	_, _ = mac.Write([]byte(account))
	_, _ = mac.Write(raw)
	index := map[string]state.App{}
	for _, app := range apps {
		index[app.WorkloadName] = app
	}
	rows := []state.ProjectReleaseSealedSecret{}
	for name, values := range request.Workloads {
		app, ok := index[name]
		if !ok || app.AccountID != account || app.Status == state.AppDeleted {
			return nil, "", fmt.Errorf("release secrets reference an unknown workload")
		}
		for key, value := range values {
			cipher, kid, hash, err := seal(key, value)
			if err != nil {
				return nil, "", fmt.Errorf("release secret encryption failed")
			}
			rows = append(rows, state.ProjectReleaseSealedSecret{Workload: name, AppID: app.ID, Key: key, Kid: kid, ValueHash: hash, Ciphertext: cipher})
		}
	}
	return rows, hex.EncodeToString(mac.Sum(nil)), nil
}

func EncryptProjectReleaseSecret(recipient *age.X25519Recipient, limits api.Limits, hmacKey []byte, key, value string) ([]byte, string, string, error) {
	cipher, err := secretbox.SealOne(recipient, key, value, limits.SecretValueMaxBytes)
	if err != nil {
		return nil, "", "", err
	}
	hash, err := secretbox.ValueFingerprint([]byte(value), hmacKey)
	return cipher, recipient.String(), hash, err
}
