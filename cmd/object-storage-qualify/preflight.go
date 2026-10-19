//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/objectqualification"
)

func remoteFlags(flags *flag.FlagSet, o *objectqualification.RemoteOptions) {
	flags.StringVar(&o.Host, "follower-ssh", "", "OpenSSH target for the disposable follower; controller stays on primary host")
	flags.StringVar(&o.Agent, "follower-agent", "", "absolute remote object-storage-qualify executable")
	flags.StringVar(&o.Daemon, "follower-daemon", "", "absolute remote object-storaged executable")
	flags.StringVar(&o.Control, "follower-ctl", "", "absolute remote object-storagectl executable")
	flags.StringVar(&o.Directory, "follower-work-dir", "", "remote new directory prefix; -local/-replica appended")
	flags.StringVar(&o.SSHConfig, "ssh-config", "", "optional OpenSSH configuration file")
	flags.BoolVar(&o.AllowSharedHost, "allow-shared-host", false, "permit equal kernel boot identities for transport validation; never evidence of host separation")
}

func runPreflight(ctx context.Context, args []string, output io.Writer) error {
	o := objectqualification.PreflightOptions{}
	flags := flag.NewFlagSet("object-storage-qualify preflight", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&o.Daemon, "daemon", "", "built local object-storaged executable")
	flags.StringVar(&o.Control, "ctl", "", "built local object-storagectl executable")
	flags.StringVar(&o.Directory, "work-dir", "", "planned new work directory on the primary data mount")
	remoteFlags(flags, &o.Remote)
	path := flags.String("report", "", "new private readiness report; never replaced")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *path == "" {
		return errors.New("preflight requires --daemon, --ctl, --work-dir and --report; see docs/owned-object-storage.md")
	}
	if _, err := os.Lstat(*path); !errors.Is(err, os.ErrNotExist) {
		return errors.New("qualification report path must not exist")
	}
	report, checkErr := objectqualification.Preflight(ctx, o)
	if err := objectqualification.WritePreflightReport(*path, report); err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(struct {
		Ready  bool   `json:"ready"`
		Report string `json:"report"`
	}{report.Ready, *path}); err != nil {
		return err
	}
	return checkErr
}
