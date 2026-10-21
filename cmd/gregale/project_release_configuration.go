package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

func readProjectReleaseConfig(path string) (json.RawMessage, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, api.ProjectReleaseConfigMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read release configuration: %w", err)
	}
	canonical, _, err := api.NormalizeProjectReleaseConfig(raw)
	return canonical, err
}
