package fcvm

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const qualificationConfigReceiptKeyPrefix = "GREGALE_INTERNAL_QUALIFICATION_RECEIPT_"

const (
	qualificationConfigReceiptTokenKey = qualificationConfigReceiptKeyPrefix + "TOKEN"
	qualificationConfigReceiptMACKey   = qualificationConfigReceiptKeyPrefix + "MAC_KEY"
)

const qualificationConfigReceiptWait = 5 * time.Second

// EnvironmentQualificationConfigReceipt is a guest-init acknowledgement that
// the private qualification guest loaded the staged configuration for one
// workload before startup. Configuration values and secret names never cross
// this receipt boundary.
type EnvironmentQualificationConfigReceipt struct {
	Token           string `json:"token"`
	Workload        string `json:"workload"`
	APIEnvSHA256    string `json:"api_env_sha256"`
	SecretsFileRead bool   `json:"secrets_file_read"`
	SecretKeysMAC   string `json:"secret_keys_mac"`
	ConfigMAC       string `json:"config_mac"`
}

type qualificationConfigReceiptExpectation struct {
	apiEnvSHA256  string
	secretsRead   bool
	secretKeysMAC string
	configMAC     string
}

type qualificationConfigReceiptWaiter struct {
	token    string
	expected map[string]qualificationConfigReceiptExpectation
	received map[string]struct{}
	result   chan error
}

func qualificationAPIEnvSHA256(env map[string]string) (string, error) {
	encoded, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// QualificationAPIEnvSHA256 returns the digest vmmd waits for the guest to
// acknowledge on a private qualification boot. It mirrors the merge semantics
// used by the guest env file: the last entry for a key wins.
func QualificationAPIEnvSHA256(entries []APIEnvEntry) (string, error) {
	env := make(map[string]string, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Key, qualificationConfigReceiptKeyPrefix) {
			return "", state.ErrInvalidArgument
		}
		env[entry.Key] = entry.Value
	}
	return qualificationAPIEnvSHA256(env)
}

func qualificationSecretKeysMAC(key []byte, keys []string) (string, error) {
	if len(key) != sha256.Size {
		return "", state.ErrInvalidArgument
	}
	keys = append([]string(nil), keys...)
	slices.Sort(keys)
	encoded, err := json.Marshal(keys)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write(encoded); err != nil {
		return "", err
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}

type qualificationSidecarConfigProjection struct {
	APIEnv     map[string]string `json:"api_env"`
	SidecarEnv map[string]string `json:"sidecar_env"`
}

func qualificationSidecarConfigMAC(key []byte, apiEnv, sidecarEnv map[string]string) (string, error) {
	if len(key) != sha256.Size {
		return "", state.ErrInvalidArgument
	}
	encoded, err := json.Marshal(qualificationSidecarConfigProjection{APIEnv: apiEnv, SidecarEnv: sidecarEnv})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write(encoded); err != nil {
		return "", err
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (m *Manager) prepareQualificationConfigReceipt(req WakeRequest) (WakeRequest, *qualificationConfigReceiptWaiter, error) {
	digest, err := QualificationAPIEnvSHA256(req.APIEnvEntries)
	if err != nil {
		return WakeRequest{}, nil, err
	}
	env := make(map[string]string, len(req.APIEnvEntries))
	for _, entry := range req.APIEnvEntries {
		env[entry.Key] = entry.Value
	}
	token := uuid.NewString()
	secretKeysKey := make([]byte, sha256.Size)
	if _, err := rand.Read(secretKeysKey); err != nil {
		return WakeRequest{}, nil, fmt.Errorf("generate qualification receipt key: %w", err)
	}
	defer clear(secretKeysKey)
	secretKeys := make([]string, 0, len(req.SealedEnvEntries))
	seen := make(map[string]struct{}, len(req.SealedEnvEntries))
	for _, entry := range req.SealedEnvEntries {
		if api.ValidateEnvKey(entry.Key) != nil || strings.HasPrefix(entry.Key, qualificationConfigReceiptKeyPrefix) {
			return WakeRequest{}, nil, state.ErrInvalidArgument
		}
		if _, exists := seen[entry.Key]; exists {
			return WakeRequest{}, nil, state.ErrInvalidArgument
		}
		seen[entry.Key] = struct{}{}
		secretKeys = append(secretKeys, entry.Key)
	}
	secretKeysMAC, err := qualificationSecretKeysMAC(secretKeysKey, secretKeys)
	if err != nil {
		return WakeRequest{}, nil, err
	}
	expected := map[string]qualificationConfigReceiptExpectation{
		WorkloadNameMain: {apiEnvSHA256: digest, secretsRead: len(req.SealedEnvEntries) > 0, secretKeysMAC: secretKeysMAC},
	}
	for _, sidecar := range req.Sidecars {
		if !validQualificationWorkloadName(sidecar.Name) || sidecar.Name == WorkloadNameMain {
			return WakeRequest{}, nil, state.ErrInvalidArgument
		}
		if len(sidecar.SealedEnv)+len(sidecar.SealedSecrets) > 0 && len(sidecar.preparedEnvJSON) == 0 {
			return WakeRequest{}, nil, fmt.Errorf("sidecar %q configuration is not prepared: %w", sidecar.Name, state.ErrInvalidArgument)
		}
		if _, exists := expected[sidecar.Name]; exists {
			return WakeRequest{}, nil, state.ErrInvalidArgument
		}
		sidecarEnv := map[string]string{}
		if len(sidecar.preparedEnvJSON) > 0 {
			if err := json.Unmarshal(sidecar.preparedEnvJSON, &sidecarEnv); err != nil || sidecarEnv == nil {
				return WakeRequest{}, nil, fmt.Errorf("sidecar %q configuration is not prepared: %w", sidecar.Name, errors.Join(err, state.ErrInvalidArgument))
			}
		}
		configMAC, err := qualificationSidecarConfigMAC(secretKeysKey, env, sidecarEnv)
		if err != nil {
			return WakeRequest{}, nil, err
		}
		expected[sidecar.Name] = qualificationConfigReceiptExpectation{configMAC: configMAC}
	}
	waiter, err := m.registerQualificationConfigReceipt(req.Instance, token, expected)
	if err != nil {
		return WakeRequest{}, nil, err
	}
	// The one-time random control key shares the existing per-wake env file so
	// its presence proves guest-init parsed the staged projection. Guest init
	// removes it before constructing the customer process environment. The
	// receipt key never crosses the guest event channel, which prevents a
	// passive observer from guessing secret key names from their MAC.
	req.APIEnvEntries = append(append([]APIEnvEntry(nil), req.APIEnvEntries...), APIEnvEntry{
		Key: qualificationConfigReceiptTokenKey, Value: token,
	}, APIEnvEntry{
		Key: qualificationConfigReceiptMACKey, Value: hex.EncodeToString(secretKeysKey),
	})
	return req, waiter, nil
}

// prepareQualificationJobConfigReceipt stages an attempt-bound receipt control
// alongside the exact environment sent to a held qualification Job. The
// controls are removed by guest-init before the command environment is built.
func (m *Manager) prepareQualificationJobConfigReceipt(instance string, env map[string]string,
	sealedEnv []SealedEnvEntry) (map[string]string, string, *qualificationConfigReceiptWaiter, error) {
	var zero map[string]string
	if instance == "" {
		return zero, "", nil, state.ErrInvalidArgument
	}
	apiEnv := make(map[string]string, len(env))
	for key, value := range env {
		if api.ValidateEnvKey(key) != nil || strings.HasPrefix(key, qualificationConfigReceiptKeyPrefix) {
			return zero, "", nil, state.ErrInvalidArgument
		}
		apiEnv[key] = value
	}
	digest, err := qualificationAPIEnvSHA256(apiEnv)
	if err != nil {
		return zero, "", nil, err
	}
	token := uuid.NewString()
	macKey := make([]byte, sha256.Size)
	if _, err := rand.Read(macKey); err != nil {
		return zero, "", nil, fmt.Errorf("generate qualification job receipt key: %w", err)
	}
	defer clear(macKey)
	secretKeys := make([]string, 0, len(sealedEnv))
	seen := make(map[string]struct{}, len(sealedEnv))
	for _, entry := range sealedEnv {
		if api.ValidateEnvKey(entry.Key) != nil || strings.HasPrefix(entry.Key, qualificationConfigReceiptKeyPrefix) {
			return zero, "", nil, state.ErrInvalidArgument
		}
		if _, duplicate := seen[entry.Key]; duplicate {
			return zero, "", nil, state.ErrInvalidArgument
		}
		seen[entry.Key] = struct{}{}
		secretKeys = append(secretKeys, entry.Key)
	}
	secretKeysMAC, err := qualificationSecretKeysMAC(macKey, secretKeys)
	if err != nil {
		return zero, "", nil, err
	}
	waiter, err := m.registerQualificationConfigReceipt(instance, token, map[string]qualificationConfigReceiptExpectation{
		WorkloadNameMain: {apiEnvSHA256: digest, secretsRead: len(sealedEnv) > 0, secretKeysMAC: secretKeysMAC},
	})
	if err != nil {
		return zero, "", nil, err
	}
	apiEnv[qualificationConfigReceiptTokenKey] = token
	apiEnv[qualificationConfigReceiptMACKey] = hex.EncodeToString(macKey)
	return apiEnv, digest, waiter, nil
}

func validQualificationWorkloadName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' || i == 0 && c == '-' {
			return false
		}
	}
	return true
}

func (m *Manager) registerQualificationConfigReceipt(instance, token string, expected map[string]qualificationConfigReceiptExpectation) (*qualificationConfigReceiptWaiter, error) {
	parsed, err := uuid.Parse(token)
	if instance == "" || err != nil || parsed == uuid.Nil || len(expected) == 0 {
		return nil, state.ErrInvalidArgument
	}
	waiter := &qualificationConfigReceiptWaiter{
		token: token, expected: expected, received: make(map[string]struct{}, len(expected)), result: make(chan error, 1),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.qualificationConfigReceiptWaiters == nil {
		m.qualificationConfigReceiptWaiters = make(map[string]*qualificationConfigReceiptWaiter)
	}
	if _, exists := m.qualificationConfigReceiptWaiters[instance]; exists {
		return nil, state.ErrConflict
	}
	m.qualificationConfigReceiptWaiters[instance] = waiter
	return waiter, nil
}

func (m *Manager) clearQualificationConfigReceipt(instance string, waiter *qualificationConfigReceiptWaiter) {
	m.mu.Lock()
	if m.qualificationConfigReceiptWaiters[instance] == waiter {
		delete(m.qualificationConfigReceiptWaiters, instance)
	}
	m.mu.Unlock()
}

// MarkEnvironmentQualificationConfigApplied accepts only the one-time receipt
// registered for the original private VM attempt. The per-instance vsock
// listener supplies instance identity; the random token fences stale frames.
func (m *Manager) MarkEnvironmentQualificationConfigApplied(instance string, receipt EnvironmentQualificationConfigReceipt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	waiter := m.qualificationConfigReceiptWaiters[instance]
	if waiter == nil {
		return state.ErrNotFound
	}
	expectation, expected := waiter.expected[receipt.Workload]
	if receipt.Token != waiter.token || !expected || receipt.APIEnvSHA256 != expectation.apiEnvSHA256 ||
		receipt.SecretsFileRead != expectation.secretsRead || !hmac.Equal([]byte(receipt.SecretKeysMAC), []byte(expectation.secretKeysMAC)) ||
		!hmac.Equal([]byte(receipt.ConfigMAC), []byte(expectation.configMAC)) {
		select {
		case waiter.result <- state.ErrConflict:
		default:
		}
		// A syntactically valid but mismatched receipt is a qualification
		// failure, not a failure of the daemon's shared event transport.
		return nil
	}
	if _, exists := waiter.received[receipt.Workload]; exists {
		return nil
	}
	waiter.received[receipt.Workload] = struct{}{}
	if len(waiter.received) == len(waiter.expected) {
		select {
		case waiter.result <- nil:
		default:
		}
	}
	return nil
}

func (m *Manager) waitForQualificationConfigReceipt(ctx context.Context, instance string, waiter *qualificationConfigReceiptWaiter) error {
	ctx, cancel := context.WithTimeout(ctx, qualificationConfigReceiptWait)
	defer cancel()
	select {
	case err := <-waiter.result:
		if err != nil {
			return fmt.Errorf("qualification guest configuration receipt did not match this attempt: %w", err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("qualification guest configuration receipt was not received for %s: %w", instance, errors.Join(ctx.Err(), context.Cause(ctx)))
	}
}
