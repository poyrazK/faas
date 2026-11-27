package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type mcpTaskReceipt struct {
	Version         int    `json:"version"`
	EndpointSHA256  string `json:"endpointSha256"`
	SubmissionKey   string `json:"submissionKey"`
	RequestSHA256   string `json:"requestSha256,omitempty"`
	CredentialProof string `json:"credentialProof,omitempty"`
}

// The receipt carries only scalar metadata and no tool arguments.
const (
	mcpTaskReceiptMaxBytes = 1024
	mcpTaskReceiptVersion  = 3
)

func mcpTaskReceiptEndpoint(endpoint string) string {
	return toolpublication.Digest([]byte(endpoint))
}

func mcpTaskSubmissionFingerprint(tool string, args map[string]any, deferFormInput bool) (string, error) {
	// Hash the decoded request as the client would marshal it. JSON object
	// keys are sorted by the encoder, and json.Number preserves wire values.
	body, err := json.Marshal(struct {
		Arguments      map[string]any `json:"arguments"`
		DeferFormInput bool           `json:"deferFormInput"`
		Tool           string         `json:"tool"`
	}{args, deferFormInput, tool})
	if err != nil {
		return "", errors.New("MCP Task request could not be fingerprinted")
	}
	return toolpublication.Digest(body), nil
}

// A server cannot check the original caller of a submission it never
// accepted. Bind the short-lived retry to the bearer credential used when
// saving the receipt, without persisting that credential.
func (receipt mcpTaskReceipt) credentialProof(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte("gregale/mcp-task-receipt/v3:" + receipt.EndpointSHA256 + receipt.SubmissionKey + receipt.RequestSHA256))
	return hex.EncodeToString(mac.Sum(nil))
}

func createMCPTaskReceipt(path, endpoint, requestSHA256, token string) (string, error) {
	key, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	receipt := mcpTaskReceipt{
		Version: mcpTaskReceiptVersion, EndpointSHA256: mcpTaskReceiptEndpoint(endpoint),
		SubmissionKey: key.String(), RequestSHA256: requestSHA256,
	}
	receipt.CredentialProof = receipt.credentialProof(token)
	body, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	if err := publishCustomerOperationReceipt(path, body); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", errors.New("task receipt already exists. Use mcp client status to recover it or --retry-submission to retry the original request")
		}
		return "", err
	}
	return receipt.SubmissionKey, nil
}

func readMCPTaskReceipt(path, endpoint string) (string, error) {
	receipt, err := loadMCPTaskReceipt(path, endpoint)
	return receipt.SubmissionKey, err
}

func loadMCPTaskReceipt(path, endpoint string) (mcpTaskReceipt, error) {
	body, err := readCustomerOperationPrivateReceiptBound(path, mcpTaskReceiptMaxBytes)
	if err != nil {
		return mcpTaskReceipt{}, err
	}
	return decodeMCPTaskReceipt(body, endpoint)
}

func readMCPTaskReceiptAt(root *os.Root, name, endpoint string) (string, error) {
	body, err := readCustomerOperationPrivateReceiptAt(root, name, mcpTaskReceiptMaxBytes)
	if err != nil {
		return "", err
	}
	receipt, err := decodeMCPTaskReceipt(body, endpoint)
	return receipt.SubmissionKey, err
}

func decodeMCPTaskReceipt(body []byte, endpoint string) (mcpTaskReceipt, error) {
	var receipt mcpTaskReceipt
	if err := decodeCustomerOperationReceipt(body, &receipt); err != nil {
		return mcpTaskReceipt{}, errors.New("invalid MCP Task receipt")
	}
	if receipt.Version < 1 || receipt.Version > mcpTaskReceiptVersion || receipt.SubmissionKey == "" {
		return mcpTaskReceipt{}, errors.New("invalid MCP Task receipt version or submission key")
	}
	if (receipt.Version == 1 && receipt.RequestSHA256 != "") || (receipt.Version >= 2 && !toolpublication.ValidID(receipt.RequestSHA256)) {
		return mcpTaskReceipt{}, errors.New("invalid MCP Task receipt request fingerprint")
	}
	if (receipt.Version < mcpTaskReceiptVersion && receipt.CredentialProof != "") ||
		(receipt.Version == mcpTaskReceiptVersion && !toolpublication.ValidID(receipt.CredentialProof)) {
		return mcpTaskReceipt{}, errors.New("invalid MCP Task receipt credential proof")
	}
	if receipt.EndpointSHA256 != mcpTaskReceiptEndpoint(endpoint) {
		return mcpTaskReceipt{}, errors.New("MCP Task receipt does not match the selected endpoint")
	}
	return receipt, nil
}

func retryMCPTaskSubmission(path, endpoint, requestSHA256, token string) (string, error) {
	receipt, err := loadMCPTaskReceipt(path, endpoint)
	if err != nil {
		return "", err
	}
	if receipt.Version < mcpTaskReceiptVersion {
		return "", errors.New("MCP Task receipt lacks a credential binding. Use mcp client status to recover it")
	}
	if receipt.RequestSHA256 != requestSHA256 {
		return "", errors.New("MCP Task request does not match the receipt. Supply the original tool, arguments and --defer-form-input setting")
	}
	key, err := uuid.Parse(receipt.SubmissionKey)
	if err != nil || key.Version() != 7 || key.Variant() != uuid.RFC4122 || key.String() != receipt.SubmissionKey {
		return "", errors.New("MCP Task submission key must be a lowercase UUIDv7")
	}
	// The server rechecks the one-minute window using the database clock.
	if !time.Now().Before(time.Unix(key.Time().UnixTime()).Add(time.Minute)) {
		return "", errors.New("MCP Task submission retry window expired. Use mcp client status to check for a retained Task")
	}
	if !hmac.Equal([]byte(receipt.CredentialProof), []byte(receipt.credentialProof(token))) {
		return "", errors.New("MCP Task submission retry requires the original client credential. Use mcp client status to check for a retained Task with a renewed credential")
	}
	return receipt.SubmissionKey, nil
}
