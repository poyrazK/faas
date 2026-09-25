package main

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

func loadServiceProxyCA(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("vmmd: read service_proxy_ca_path: %w", err)
	}
	if len(data) == 0 || len(data) > 64*1024 {
		return nil, errors.New("vmmd: service_proxy_ca_path must contain 1..65536 bytes")
	}
	rest := data
	count := 0
	for len(strings.TrimSpace(string(rest))) > 0 {
		block, next := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("vmmd: service_proxy_ca_path must contain only PEM CA certificates")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid {
			return nil, errors.New("vmmd: service_proxy_ca_path contains a non-CA certificate")
		}
		count++
		rest = next
	}
	if count == 0 {
		return nil, errors.New("vmmd: service_proxy_ca_path contains no CA certificates")
	}
	return data, nil
}
