// Package executionadmission binds bounded source Run requests without storing
// source, input, artifact capabilities, or raw idempotency keys.
package executionadmission

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

// Fingerprint canonicalizes the decoded request before defaults or artifact
// expansion. All accepted body sizes are covered. A domain-separated host MAC
// protects low-entropy secrets from offline guessing against a database copy.
func Fingerprint(request api.CreateExecutionRequest, hostKey []byte) ([32]byte, error) {
	return fingerprintValue(request, hostKey, "gregale source Run admission v1")
}

// TaskFingerprint binds a named invocation independently of its stored source.
func TaskFingerprint(request api.InvokeExecutionTaskRequest, hostKey []byte) ([32]byte, error) {
	return fingerprintValue(request, hostKey, "gregale named Run task admission v1")
}

func fingerprintValue(request any, hostKey []byte, domain string) ([32]byte, error) {
	var result [32]byte
	if len(hostKey) == 0 {
		return result, errors.New("execution admission HMAC key unavailable")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return result, err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return result, err
	}
	derive := hmac.New(sha256.New, hostKey)
	_, _ = derive.Write([]byte(domain))
	key := derive.Sum(nil)
	keyID := sha256.Sum256(key)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(canonical)
	copy(result[:4], keyID[:4])
	copy(result[4:], mac.Sum(nil)[:28])
	return result, nil
}

func ValidKey(key string) bool {
	if len(key) == 0 || len(key) > api.ExecutionIdempotencyKeyMaxBytes {
		return false
	}
	for i := range key {
		if key[i] < '!' || key[i] > '~' {
			return false
		}
	}
	return true
}

// TaskToolFingerprint binds the provider call identity as well as the task
// arguments. A refreshed callback token may replay the original call, while a
// reused tool-call ID cannot redirect its result to another provider call.
func TaskToolFingerprint(request api.InvokeExecutionTaskRequest, providerCallID string, hostKey []byte) ([32]byte, error) {
	return fingerprintValue(struct {
		Request        api.InvokeExecutionTaskRequest `json:"request"`
		ProviderCallID string                         `json:"provider_call_id"`
	}{request, providerCallID}, hostKey, "gregale named Run task tool admission v1")
}
