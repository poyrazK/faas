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
	APIEnvSHA256    string `json:"api_env_sha256"`
	SecretsFileRead bool   `json:"secrets_file_read"`
	SecretKeysMAC   string `json:"secret_keys_mac"`
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
	parsed, err := uuid.Parse(token)
	macKey, keyErr := hex.DecodeString(encodedMACKey)
	if controls != 2 || err != nil || parsed == uuid.Nil || keyErr != nil || len(macKey) != sha256.Size {
		return "", nil, fmt.Errorf("invalid qualification receipt control")
	}
	return token, macKey, nil
}

func qualificationConfigReceiptFrame(token string, macKey []byte, apiEnv, secrets map[string]string) ([]byte, error) {
	if len(macKey) != sha256.Size {
		return nil, fmt.Errorf("qualification receipt MAC key has invalid length")
	}
	apiJSON, err := json.Marshal(apiEnv)
	if err != nil {
		return nil, err
	}
	apiDigest := sha256.Sum256(apiJSON)
	secretKeys := make([]string, 0, len(secrets))
	for key := range secrets {
		secretKeys = append(secretKeys, key)
	}
	sort.Strings(secretKeys)
	secretJSON, err := json.Marshal(secretKeys)
	if err != nil {
		return nil, err
	}
	secretMAC := hmac.New(sha256.New, macKey)
	if _, err := secretMAC.Write(secretJSON); err != nil {
		return nil, err
	}
	receipt := qualificationConfigReceiptWire{
		Token: token, APIEnvSHA256: hex.EncodeToString(apiDigest[:]),
		SecretsFileRead: len(secrets) > 0, SecretKeysMAC: hex.EncodeToString(secretMAC.Sum(nil)),
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	return append([]byte{vsockQualificationConfigReceiptType}, payload...), nil
}

func emitQualificationConfigReceipt(log *slog.Logger, token string, macKey []byte, apiEnv, secrets map[string]string) {
	if token == "" {
		return
	}
	frame, err := qualificationConfigReceiptFrame(token, macKey, apiEnv, secrets)
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
