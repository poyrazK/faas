//go:build linux

package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	qualificationConfigReceiptKeyPrefix      = "GREGALE_INTERNAL_QUALIFICATION_RECEIPT_"
	qualificationConfigReceiptTokenKey       = qualificationConfigReceiptKeyPrefix + "TOKEN"
	qualificationConfigReceiptMACKey         = qualificationConfigReceiptKeyPrefix + "MAC_KEY"
	vsockQualificationConfigReceiptType byte = 0x09
)

type qualificationConfigReceiptWire struct {
	Token           string `json:"token"`
	Workload        string `json:"workload"`
	APIEnvSHA256    string `json:"api_env_sha256"`
	SecretsFileRead bool   `json:"secrets_file_read"`
	SecretKeysMAC   string `json:"secret_keys_mac"`
	ConfigMAC       string `json:"config_mac"`
}

type qualificationSidecarConfigProjection struct {
	APIEnv     map[string]string `json:"api_env"`
	SidecarEnv map[string]string `json:"sidecar_env"`
}

// takeQualificationConfigReceiptControl removes both host-generated controls
// before apiEnv is merged into any customer process environment.
func takeQualificationConfigReceiptControl(apiEnv map[string]string) (string, []byte, error) {
	token, encodedMACKey := "", ""
	controls := 0
	for key, value := range apiEnv {
		if !strings.HasPrefix(key, qualificationConfigReceiptKeyPrefix) {
			continue
		}
		delete(apiEnv, key)
		controls++
		switch key {
		case qualificationConfigReceiptTokenKey:
			token = value
		case qualificationConfigReceiptMACKey:
			encodedMACKey = value
		default:
			return "", nil, fmt.Errorf("unknown qualification receipt control")
		}
	}
	if controls == 0 {
		return "", nil, nil
	}
	if controls != 2 {
		return "", nil, fmt.Errorf("invalid qualification receipt control")
	}
	return parseQualificationConfigReceiptControl(token, encodedMACKey)
}

func parseQualificationConfigReceiptControl(token, encodedMACKey string) (string, []byte, error) {
	parsed, err := uuid.Parse(token)
	macKey, keyErr := hex.DecodeString(encodedMACKey)
	if err != nil || parsed == uuid.Nil || keyErr != nil || len(macKey) != sha256.Size {
		clear(macKey)
		return "", nil, fmt.Errorf("invalid qualification receipt control")
	}
	return token, macKey, nil
}

// qualificationMainConfigBasis is what a main-workload receipt attests: the
// API env digest and the secret key names, never secret values. Keeping only
// this lets a restored guest re-attest its boot configuration.
type qualificationMainConfigBasis struct {
	apiEnvSHA256 string
	secretKeys   []string
}

func newQualificationMainConfigBasis(apiEnv, secrets map[string]string) (qualificationMainConfigBasis, error) {
	apiJSON, err := json.Marshal(apiEnv)
	if err != nil {
		return qualificationMainConfigBasis{}, err
	}
	apiDigest := sha256.Sum256(apiJSON)
	secretKeys := make([]string, 0, len(secrets))
	for key := range secrets {
		secretKeys = append(secretKeys, key)
	}
	sort.Strings(secretKeys)
	return qualificationMainConfigBasis{apiEnvSHA256: hex.EncodeToString(apiDigest[:]), secretKeys: secretKeys}, nil
}

func (b qualificationMainConfigBasis) receipt(token string, macKey []byte) (qualificationConfigReceiptWire, error) {
	secretJSON, err := json.Marshal(b.secretKeys)
	if err != nil {
		return qualificationConfigReceiptWire{}, err
	}
	secretMAC := hmac.New(sha256.New, macKey)
	if _, err := secretMAC.Write(secretJSON); err != nil {
		return qualificationConfigReceiptWire{}, err
	}
	return qualificationConfigReceiptWire{Token: token, Workload: "main", APIEnvSHA256: b.apiEnvSHA256,
		SecretsFileRead: len(b.secretKeys) > 0, SecretKeysMAC: hex.EncodeToString(secretMAC.Sum(nil))}, nil
}

func qualificationConfigReceiptFrame(workload, token string, macKey []byte, apiEnv, secrets, sidecarEnv map[string]string) ([]byte, error) {
	if len(macKey) != sha256.Size {
		return nil, fmt.Errorf("qualification receipt MAC key has invalid length")
	}
	receipt := qualificationConfigReceiptWire{Token: token, Workload: workload}
	if workload == "main" {
		basis, err := newQualificationMainConfigBasis(apiEnv, secrets)
		if err != nil {
			return nil, err
		}
		if receipt, err = basis.receipt(token, macKey); err != nil {
			return nil, err
		}
	} else {
		envJSON, err := json.Marshal(qualificationSidecarConfigProjection{APIEnv: apiEnv, SidecarEnv: sidecarEnv})
		if err != nil {
			return nil, err
		}
		configMAC := hmac.New(sha256.New, macKey)
		if _, err := configMAC.Write(envJSON); err != nil {
			return nil, err
		}
		receipt.ConfigMAC = hex.EncodeToString(configMAC.Sum(nil))
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	return append([]byte{vsockQualificationConfigReceiptType}, payload...), nil
}

func emitQualificationConfigReceipt(log *slog.Logger, workload, token string, macKey []byte, apiEnv, secrets, sidecarEnv map[string]string) {
	if token == "" {
		return
	}
	frame, err := qualificationConfigReceiptFrame(workload, token, macKey, apiEnv, secrets, sidecarEnv)
	if err != nil {
		if log != nil {
			log.Warn("qualification configuration receipt could not be encoded", "err", err)
		}
		return
	}
	var sendErr error
	for attempt := 0; attempt < 3; attempt++ {
		sendErr = sendGuestEventFrame(frame, time.Second)
		if sendErr == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	if log != nil {
		log.Warn("qualification configuration receipt could not reach host", "err", sendErr)
	}
}

const (
	qualificationRestoreReceiptWindow = 30 * time.Second
	qualificationRestoreReceiptRetry  = 100 * time.Millisecond
)

// qualificationRestoreBasis is the main workload's boot-time receipt basis.
// It is set only after a qualification boot emitted its receipt, so only such
// a guest can re-attest after a native qualification restore.
var qualificationRestoreBasis atomic.Pointer[qualificationMainConfigBasis]

func rememberQualificationRestoreBasis(apiEnv, secrets map[string]string) error {
	basis, err := newQualificationMainConfigBasis(apiEnv, secrets)
	if err != nil {
		return err
	}
	qualificationRestoreBasis.Store(&basis)
	return nil
}

// emitQualificationRestoreReceiptFn is the resume handler's seam.
var emitQualificationRestoreReceiptFn = func(log *slog.Logger, token, encodedMACKey string) {
	emitQualificationRestoreReceipt(log, token, encodedMACKey, sendGuestEventFrame, qualificationRestoreReceiptWindow, qualificationRestoreReceiptRetry)
}

// emitQualificationRestoreReceipt re-attests the boot configuration under the
// fresh control a native qualification restore sent with its resume hook
// (ADR 20261023093000). vmmd opens the restore's guest-event channel only
// after the hook is acknowledged, so this retries for a bounded window rather
// than the boot receipt's three attempts.
func emitQualificationRestoreReceipt(log *slog.Logger, token, encodedMACKey string,
	send func([]byte, time.Duration) error, window, retry time.Duration) {
	token, macKey, err := parseQualificationConfigReceiptControl(token, encodedMACKey)
	if err != nil {
		log.Warn("qualification restore receipt control is invalid")
		return
	}
	basis := qualificationRestoreBasis.Load()
	if basis == nil {
		clear(macKey)
		log.Warn("qualification restore receipt requested by a guest that booted without one")
		return
	}
	receipt, err := basis.receipt(token, macKey)
	clear(macKey)
	if err != nil {
		log.Warn("qualification restore receipt could not be encoded", "err", err)
		return
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		log.Warn("qualification restore receipt could not be encoded", "err", err)
		return
	}
	frame := append([]byte{vsockQualificationConfigReceiptType}, payload...)
	deadline := time.Now().Add(window)
	for {
		sendErr := send(frame, time.Second)
		if sendErr == nil {
			return
		}
		if !time.Now().Before(deadline) {
			log.Warn("qualification restore receipt could not reach host", "err", sendErr)
			return
		}
		time.Sleep(retry)
	}
}
