package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"

	"github.com/onebox-faas/faas/pkg/objectnode"
)

func runRetention(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("data-dir", "", "offline node directory")
	var replica, hash string
	sequence := int64(-1)
	if args[0] == "compact" {
		flags.Int64Var(&sequence, "through", -1, "last sequence to prune; default is the largest safe prefix")
	} else {
		flags.StringVar(&replica, "replica-id", "", "follower node UUID")
		if args[0] == "replica-register" {
			flags.Int64Var(&sequence, "sequence", -1, "follower's durable applied sequence from replica-status")
			flags.StringVar(&hash, "hash", "", "follower's applied hash from replica-status")
		}
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *directory == "" || flags.NArg() != 0 {
		return errors.New("object-storagectl: specify --data-dir and no positional arguments")
	}
	var result any
	var err error
	switch args[0] {
	case "compact":
		result, err = objectnode.CompactJournal(ctx, *directory, sequence)
	case "replica-register":
		result, err = objectnode.RegisterFollower(ctx, *directory, replica, sequence, hash)
	case "replica-retire":
		result, err = objectnode.RetireFollower(ctx, *directory, replica)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}
