package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
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

type bucketDefaultEncryptionClient interface {
	GetObjectBucketEncryption(context.Context, string, string) (api.ObjectBucketEncryption, error)
	PutObjectBucketEncryption(context.Context, string, string, api.ObjectEncryption) (api.ObjectBucketEncryption, error)
	DeleteObjectBucketEncryption(context.Context, string, string) (api.ObjectBucketEncryption, error)
}

func cmdBucketDefaultEncryption(args []string) int {
	if len(args) < 3 || !api.ValidAppSlug(args[1]) {
		return bucketDefaultEncryptionUsage()
	}
	if _, err := uuid.Parse(args[2]); err != nil {
		return bucketDefaultEncryptionUsage()
	}
	if _, err := bucketDefaultEncryptionSelection(args); err != nil {
		return bucketDefaultEncryptionUsage()
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := runBucketDefaultEncryption(context.Background(), c, args)
	if err != nil {
		return printErr("Could not configure bucket encryption", err)
	}
	return jsonOut(writeJSON(out))
}

func runBucketDefaultEncryption(ctx context.Context, c bucketDefaultEncryptionClient, args []string) (api.ObjectBucketEncryption, error) {
	e, err := bucketDefaultEncryptionSelection(args)
	if err != nil {
		return api.ObjectBucketEncryption{}, err
	}
	switch args[0] {
	case "status":
		return c.GetObjectBucketEncryption(ctx, args[1], args[2])
	case "clear":
		return c.DeleteObjectBucketEncryption(ctx, args[1], args[2])
	default:
		return c.PutObjectBucketEncryption(ctx, args[1], args[2], e)
	}
}

func bucketDefaultEncryptionSelection(args []string) (api.ObjectEncryption, error) {
	e := api.ObjectEncryption{}
	if len(args) < 3 {
		return e, fmt.Errorf("invalid encryption arguments")
	}
	switch args[0] {
	case "status", "clear":
		if len(args) == 3 {
			return e, nil
		}
	case "AES256":
		if len(args) == 3 {
			return api.ObjectEncryption{Algorithm: "AES256"}, nil
		}
	case "aws:kms", "aws:kms:dsse":
		if len(args) != 4 && len(args) != 5 {
			return e, fmt.Errorf("invalid encryption arguments")
		}
		e.Algorithm, e.KeyID = args[0], args[3]
		if len(args) == 5 {
			if e.Algorithm != "aws:kms" || args[4] != "true" && args[4] != "false" {
				return e, fmt.Errorf("invalid bucket key option")
			}
			value := args[4] == "true"
			e.BucketKeyEnabled = &value
		}
		if e.Valid() {
			return e, nil
		}
	}
	return e, fmt.Errorf("invalid bucket encryption selection")
}

func bucketDefaultEncryptionUsage() int {
	PrintUsage(osStderr, "usage: gregale bucket encryption <status|clear|AES256|aws:kms|aws:kms:dsse> <app> <bucket-id> [owned-key-ref] [bucket-key-enabled]", "bucket")
	return 1
}
