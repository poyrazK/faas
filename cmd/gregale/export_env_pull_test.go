package main

import (
	"context"
	"testing"
)

func StageEnvPullWriteForTest(t *testing.T, path string, contents []byte) (func(context.Context) error, func(), string, error) {
	t.Helper()
	before, err := readEnvPullFile(path)
	if err != nil {
		return nil, nil, "", err
	}
	staged, err := stageEnvPullFile(before, contents)
	if err != nil {
		return nil, nil, "", err
	}
	t.Cleanup(staged.cleanup)
	return staged.commit, staged.cleanup, staged.temp, nil
}
