package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdRealtimePush(args []string) int {
	if len(args) == 0 {
		return printErr("Invalid push command", fmt.Errorf("use providers|configure|devices|register|unregister|deliveries|preferences|set-preferences"))
	}
	action := args[0]
	fs := newFlagSet("realtime push "+action, flag.ContinueOnError)
	principal := fs.String("principal", "", "verified principal")
	device := fs.String("device", "", "stable device name")
	provider := fs.String("provider", "", "fcm, apns, or webpush")
	file := fs.String("file", "-", "request JSON file; - reads stdin")
	enabled := fs.Bool("enabled", true, "enable configured provider")
	if parseInterspersed(fs, args[1:]) != nil {
		return 1
	}
	if fs.NArg() != 2 {
		return printErr("Invalid push command", fmt.Errorf("requires APP ENDPOINT"))
	}
	if action != "providers" && action != "configure" && api.ValidateRealtimePrincipal(*principal) != nil {
		return printErr("Invalid push command", fmt.Errorf("requires --principal ID"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var result any
	read := func() (json.RawMessage, error) {
		var reader io.Reader = os.Stdin
		if *file != "-" {
			f, e := openCustomerFile(*file)
			if e != nil {
				return nil, e
			}
			defer func() { _ = f.Close() }()
			reader = f
		}
		data, e := io.ReadAll(io.LimitReader(reader, 16385))
		if e != nil {
			return nil, e
		}
		if len(data) > 16384 || !json.Valid(data) {
			return nil, fmt.Errorf("invalid or oversized JSON")
		}
		return data, nil
	}
	switch action {
	case "preferences":
		result, err = client.GetManagedRealtimeNotificationPreferences(ctx, fs.Arg(0), fs.Arg(1), *principal)
	case "set-preferences":
		var data json.RawMessage
		data, err = read()
		if err == nil {
			var p api.RealtimeNotificationPreferences
			p, err = api.DecodeRealtimeNotificationPreferences(data)
			if err == nil {
				result, err = client.PutManagedRealtimeNotificationPreferences(ctx, fs.Arg(0), fs.Arg(1), *principal, p)
			}
		}

	case "providers":
		result, err = client.ListManagedRealtimePushProviders(ctx, fs.Arg(0), fs.Arg(1))
	case "configure":
		var data json.RawMessage
		data, err = read()
		if err == nil {
			err = client.PutManagedRealtimePushProvider(ctx, fs.Arg(0), fs.Arg(1), *provider, api.ManagedRealtimePushProviderRequest{Config: data, Enabled: *enabled})
		}
	case "devices":
		result, err = client.ListManagedRealtimePushDevices(ctx, fs.Arg(0), fs.Arg(1), *principal)
	case "deliveries":
		result, err = client.ListManagedRealtimePushDeliveries(ctx, fs.Arg(0), fs.Arg(1), *principal)
	case "register":
		if *device == "" {
			return printErr("Invalid push command", fmt.Errorf("requires --device NAME"))
		}
		var data json.RawMessage
		data, err = read()
		if err == nil {
			var req api.ManagedRealtimePushDeviceRequest
			if err = json.Unmarshal(data, &req); err == nil {
				err = client.PutManagedRealtimePushDevice(ctx, fs.Arg(0), fs.Arg(1), *principal, *device, req)
			}
		}
	case "unregister":
		if *device == "" {
			return printErr("Invalid push command", fmt.Errorf("requires --device NAME"))
		}
		err = client.DeleteManagedRealtimePushDevice(ctx, fs.Arg(0), fs.Arg(1), *principal, *device)
	default:
		return printErr("Invalid push command", fmt.Errorf("unknown push subcommand"))
	}
	if err != nil {
		return printErr("Could not access push", err)
	}
	if result != nil {
		return jsonOut(writeJSON(result))
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]bool{"ok": true}))
	}
	PrintOK(osStdout, "Push settings updated.")
	return 0
}
