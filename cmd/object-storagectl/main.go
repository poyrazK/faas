// object-storagectl creates, verifies and restores offline object-node backups.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/onebox-faas/faas/pkg/objectnode"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("object storage recovery command failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) > 0 && (args[0] == "recovery-enable" || args[0] == "recovery-status" || args[0] == "recovery-settle" || args[0] == "recovery-collect") {
		return runRecovery(ctx, args, output)
	}
	if len(args) > 0 && (args[0] == "compact" || args[0] == "replica-register" || args[0] == "replica-retire") {
		return runRetention(ctx, args, output)
	}
	if len(args) > 0 && (args[0] == "replication-enable" || args[0] == "replication-durability" || args[0] == "replica-bootstrap" || args[0] == "replica-catch-up" || args[0] == "replica-status" || args[0] == "primary-fence" || args[0] == "replica-promote") {
		return runReplication(ctx, args, output)
	}
	if len(args) == 0 || args[0] != "backup" && args[0] != "verify" && args[0] != "restore" {
		return errors.New("usage: object-storagectl {compact|replica-register|replica-retire|backup|verify|restore|replication-enable|replication-durability|replica-bootstrap|replica-catch-up|replica-status|primary-fence|replica-promote} [flags]; see docs/owned-object-storage.md")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	backup := flags.String("backup-dir", "", "backup directory (new destination for backup, existing source for verify/restore)")
	var data string
	if args[0] != "verify" {
		flags.StringVar(&data, "data-dir", "", "offline node data directory (new destination for restore)")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *backup == "" || args[0] != "verify" && data == "" {
		return errors.New("object-storagectl: specify --backup-dir and, for backup/restore, --data-dir; no positional arguments are accepted")
	}
	var info objectnode.BackupInfo
	var err error
	switch args[0] {
	case "backup":
		info, err = objectnode.Backup(ctx, data, *backup)
	case "verify":
		info, err = objectnode.VerifyBackup(ctx, *backup)
	case "restore":
		info, err = objectnode.RestoreBackup(ctx, *backup, data)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(info)
}
