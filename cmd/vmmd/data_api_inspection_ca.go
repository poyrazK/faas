package main

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/dataapiinspectioncommand"
)

// loadDataAPIInspectionCA reads an operator-owned public trust bundle. A
// missing configuration disables this inspection without affecting app tasks.
func loadDataAPIInspectionCA(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	file, err := os.Open(path) //nolint:forbidigo // The daemon reads an operator-owned host trust bundle.
	if err != nil {
		return nil, errors.New("vmmd: cannot read data API inspection CA")
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, dataapiinspectioncommand.MaxTrustBundleBytes+1))
	if err != nil {
		return nil, errors.New("vmmd: cannot read data API inspection CA")
	}
	if _, err := dataapiinspectioncommand.ParseTrustBundle(data); err != nil {
		return nil, errors.New("vmmd: invalid data API inspection CA")
	}
	return data, nil
}
