package main

import (
	"fmt"
	"os"
)

// Guided workflows persist these identities before source can be submitted.
// The callback must succeed before the corresponding remote request proceeds.
type deploySubmission struct {
	Key, Transport, AppID string
	TotalSize             int64
}

type deployUploadIdentity struct {
	UploadID  string
	TotalSize int64
}

func (e deployExecution) preparingSubmission(key, transport, appID, archive string) error {
	if e.onSubmission == nil {
		return nil
	}
	info, err := os.Stat(archive)
	if err != nil {
		return fmt.Errorf("inspect source submission: %w", err)
	}
	return e.onSubmission(deploySubmission{Key: key, Transport: transport, AppID: appID, TotalSize: info.Size()})
}
