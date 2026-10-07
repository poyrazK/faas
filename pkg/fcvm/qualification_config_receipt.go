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

// EnvironmentQualificationConfigReceipt is the guest-init acknowledgement
// that the private qualification guest loaded its staged non-secret API
// environment before starting the workload. Secret values never cross this
// receipt boundary.
type EnvironmentQualificationConfigReceipt struct {
	Token           string `json:"token"`
	APIEnvSHA256    string `json:"api_env_sha256"`
	SecretsFileRead bool   `json:"secrets_file_read"`
	SecretKeysMAC   string `json:"secret_keys_mac"`
}

type qualificationConfigReceiptWaiter struct {
	token         string
	apiEnvSHA256  string
	secretsRead   bool
	secretKeysMAC string
	result        chan error
}

func qualificationAPIEnvSHA256(env map[string]string) (string, error) {
	encoded, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
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

func (m *Manager) prepareQualificationConfigReceipt(req WakeRequest) (WakeRequest, *qualificationConfigReceiptWaiter, error) {
	env := make(map[string]string, len(req.APIEnvEntries))
	for _, entry := range req.APIEnvEntries {
		if strings.HasPrefix(entry.Key, qualificationConfigReceiptKeyPrefix) {
			return WakeRequest{}, nil, state.ErrInvalidArgument
		}
		env[entry.Key] = entry.Value
	}
	digest, err := qualificationAPIEnvSHA256(env)
	if err != nil {
		return WakeRequest{}, nil, err
	}
	token := uuid.NewString()
	secretKeysKey := make([]byte, sha256.Size)
	if _, err := rand.Read(secretKeysKey); err != nil {
		return WakeRequest{}, nil, fmt.Errorf("generate qualification receipt key: %w", err)
	}
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
	defer clear(secretKeysKey)
	waiter, err := m.registerQualificationConfigReceipt(req.Instance, token, digest, len(req.SealedEnvEntries) > 0, secretKeysMAC)
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

func (m *Manager) registerQualificationConfigReceipt(instance, token, digest string, secretsRead bool, secretKeysMAC string) (*qualificationConfigReceiptWaiter, error) {
	parsed, err := uuid.Parse(token)
	if instance == "" || err != nil || parsed == uuid.Nil || digest == "" {
		return nil, state.ErrInvalidArgument
	}
	waiter := &qualificationConfigReceiptWaiter{
		token: token, apiEnvSHA256: digest, secretsRead: secretsRead,
		secretKeysMAC: secretKeysMAC, result: make(chan error, 1),
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
	if receipt.Token != waiter.token || receipt.APIEnvSHA256 != waiter.apiEnvSHA256 || receipt.SecretsFileRead != waiter.secretsRead ||
		receipt.SecretKeysMAC != waiter.secretKeysMAC {
		select {
		case waiter.result <- state.ErrConflict:
		default:
		}
		// A syntactically valid but mismatched receipt is a qualification
		// failure, not a failure of the daemon's shared event transport.
		return nil
	}
	select {
	case waiter.result <- nil:
	default:
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
