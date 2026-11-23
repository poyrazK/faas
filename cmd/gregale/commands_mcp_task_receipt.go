package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"

	"github.com/google/uuid"
)

type mcpTaskReceipt struct {
	Version        int    `json:"version"`
	EndpointSHA256 string `json:"endpointSha256"`
	SubmissionKey  string `json:"submissionKey"`
}

// The receipt has three scalar fields and carries no tool arguments.
const mcpTaskReceiptMaxBytes = 1024

func mcpTaskReceiptEndpoint(endpoint string) string {
	digest := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(digest[:])
}

func createMCPTaskReceipt(path, endpoint string) (string, error) {
	key, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	receipt := mcpTaskReceipt{Version: 1, EndpointSHA256: mcpTaskReceiptEndpoint(endpoint), SubmissionKey: key.String()}
	body, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	if err := publishCustomerOperationReceipt(path, body); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", errors.New("task receipt already exists. Use mcp client status to recover it")
		}
		return "", err
	}
	return receipt.SubmissionKey, nil
}

func readMCPTaskReceipt(path, endpoint string) (string, error) {
	body, err := readCustomerOperationPrivateReceiptBound(path, mcpTaskReceiptMaxBytes)
	if err != nil {
		return "", err
	}
	return decodeMCPTaskReceipt(body, endpoint)
}

func readMCPTaskReceiptAt(root *os.Root, name, endpoint string) (string, error) {
	body, err := readCustomerOperationPrivateReceiptAt(root, name, mcpTaskReceiptMaxBytes)
	if err != nil {
		return "", err
	}
	return decodeMCPTaskReceipt(body, endpoint)
}

func decodeMCPTaskReceipt(body []byte, endpoint string) (string, error) {
	var receipt mcpTaskReceipt
	if err := decodeCustomerOperationReceipt(body, &receipt); err != nil {
		return "", errors.New("invalid MCP Task receipt")
	}
	if receipt.Version != 1 || receipt.SubmissionKey == "" {
		return "", errors.New("invalid MCP Task receipt version or submission key")
	}
	if receipt.EndpointSHA256 != mcpTaskReceiptEndpoint(endpoint) {
		return "", errors.New("MCP Task receipt does not match the selected endpoint")
	}
	return receipt.SubmissionKey, nil
}
