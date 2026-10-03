package main

import (
	"context"

	"github.com/google/uuid"
)

func cmdBucketEncryptionKeys(args []string) int {
	if len(args) != 2 || args[0] == "" {
		PrintUsage(osStderr, "usage: gregale bucket encryption-keys <app> <bucket-id>", "bucket")
		return 1
	}
	if _, err := uuid.Parse(args[1]); err != nil {
		return printErr("Invalid bucket ID", err)
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := c.GetObjectBucketEncryptionCapabilities(context.Background(), args[0], args[1])
	if err != nil {
		return printErr("Could not list encryption keys", err)
	}
	return jsonOut(writeJSON(out))
}
