package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// retryBudgetRedisURL accepts the legacy direct environment variable and a
// systemd credential path. The two sources are mutually exclusive so a stale
// host-local env file cannot silently override the fleet credential.
func retryBudgetRedisURL(getenv func(string) string) (string, error) {
	direct := strings.TrimSpace(getenv("FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL"))
	path := strings.TrimSpace(getenv("FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL_FILE"))
	if direct != "" && path != "" {
		return "", errors.New("retry budget Redis URL and URL file are both configured")
	}
	if path == "" {
		return direct, nil
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read retry budget Redis URL file: %w", err)
	}
	url := strings.TrimSpace(string(value))
	if url == "" {
		return "", errors.New("retry budget Redis URL file is empty")
	}
	return url, nil
}
