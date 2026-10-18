package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func encryptionCLIRoute(route string) bool {
	return route == internalmail.EncryptionPath || strings.HasPrefix(route, internalmail.EncryptionPath+"/")
}

func parseEncryptionCommand(args []string, diagnostics io.Writer) (command, error) {
	fs := flag.NewFlagSet("mailctl encryption "+args[1], flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	input := internalmail.EncryptionRequest{}
	revision := int64(-1)
	batchSize := api.InternalMailEncryptionBatchSize
	switch args[1] {
	case "status":
	case "preview":
		fs.IntVar(&batchSize, "batch-size", batchSize, "maximum blobs to inspect (1..25)")
	case "activate", "retire", "rewrap":
		fs.StringVar(&input.KeyID, "key-id", "", "encryption key ID from status")
		fs.Int64Var(&revision, "expected-revision", -1, "selection revision from encryption status")
		addAuditFlags(fs, &input.OperatorAuditInput)
		if args[1] == "rewrap" {
			fs.IntVar(&input.BatchSize, "batch-size", batchSize, "maximum blobs to re-encrypt (1..25)")
		}
	default:
		return command{}, fmt.Errorf("unknown encryption command; use mailctl help")
	}
	if err := fs.Parse(args[2:]); err != nil {
		return command{}, err
	}
	if fs.NArg() != 0 {
		return command{}, fmt.Errorf("positional arguments are not accepted")
	}
	if args[1] == "status" {
		return command{http.MethodGet, internalmail.EncryptionPath, nil}, nil
	}
	if args[1] == "preview" {
		if batchSize < 1 || batchSize > api.InternalMailMaxEncryptionBatchSize {
			return command{}, fmt.Errorf("invalid encryption batch size")
		}
		return command{http.MethodPost, internalmail.EncryptionPath + "/preview", struct {
			BatchSize int `json:"batch_size"`
		}{batchSize}}, nil
	}
	input.ExpectedRevision = &revision
	if input.Validate(args[1]) != nil {
		return command{}, fmt.Errorf("encryption mutation requires a key ID, current revision, bounded batch and all audit flags")
	}
	return command{http.MethodPost, internalmail.EncryptionPath + "/" + args[1], input}, nil
}
