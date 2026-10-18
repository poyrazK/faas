package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
	"golang.org/x/sys/unix"
)

func runRecovery(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	data := flags.String("data-dir", "", "stopped primary node data directory")
	var settlement, cursor string
	var limit int
	if args[0] == "recovery-settle" {
		flags.StringVar(&settlement, "settlement-file", "", "exact acknowledgement of an already durable gateway settlement")
	}
	if args[0] == "recovery-collect" {
		flags.StringVar(&cursor, "cursor", "", "next_cursor from the previous maintenance page")
		flags.IntVar(&limit, "limit", api.ObjectNodeRecoveryBatch, "maximum identities examined in this page")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *data == "" || args[0] == "recovery-settle" && settlement == "" || args[0] == "recovery-collect" && (limit < 1 || limit > api.ObjectNodeRecoveryMaxBatch) {
		return errors.New("object-storagectl: specify --data-dir and valid recovery flags; see docs/owned-object-storage.md")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if info, err := os.Lstat(*data); err != nil || !info.IsDir() {
		return errors.New("object-storagectl: recovery maintenance requires an existing node directory")
	}
	s, err := objectnode.Open(*data)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	var result any
	switch args[0] {
	case "recovery-enable":
		result, err = s.EnableRecoveryRetention()
	case "recovery-status":
		result, err = s.RecoveryRetentionStatus()
	case "recovery-collect":
		result, err = s.CollectRecovery(ctx, cursor, limit)
	case "recovery-settle":
		var a objectrecovery.Settlement
		a, err = readRecoverySettlement(settlement)
		if err == nil {
			result, err = s.AcknowledgeRecovery(a)
		}
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func readRecoverySettlement(path string) (objectrecovery.Settlement, error) {
	var a objectrecovery.Settlement
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return a, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > api.ObjectNodeRecoveryRequestMaxBytes {
		return a, objectnode.ErrInvalid
	}
	body, err := io.ReadAll(io.LimitReader(f, api.ObjectNodeRecoveryRequestMaxBytes+1))
	if err != nil || len(body) > api.ObjectNodeRecoveryRequestMaxBytes {
		return a, objectnode.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&a) != nil || decoder.Decode(new(any)) != io.EOF {
		return a, objectnode.ErrInvalid
	}
	return a, nil
}
