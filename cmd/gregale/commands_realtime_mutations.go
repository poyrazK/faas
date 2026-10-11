package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"os"
)

func cmdRealtimeMutate(args []string, remove bool) int {
	fs := newFlagSet("realtime edit-message", flag.ContinueOnError)
	channel := fs.String("channel", "", "retained channel")
	principal := fs.String("principal", "", "principal inbox")
	id := fs.String("message-id", "", "stable publisher message ID")
	version := fs.Int64("expected-version", 0, "current message version, required")
	data := fs.String("data", "", "replacement payload")
	stdin := fs.Bool("data-stdin", false, "read replacement bytes from stdin")
	binary := fs.Bool("binary", false, "replacement is binary")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 2 || (*channel == "") == (*principal == "") || *id == "" || *version < 1 {
		return printErr("Invalid mutation", fmt.Errorf("use APP ENDPOINT, exactly one of --channel or --principal, --message-id and --expected-version"))
	}
	provided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "data" {
			provided = true
		}
	})
	if remove && (*stdin || provided || *binary) {
		return printErr("Invalid delete", fmt.Errorf("deletes do not accept payload flags"))
	}
	var payload []byte
	if !remove {
		if *stdin == provided {
			return printErr("Invalid edit", fmt.Errorf("use exactly one of --data or --data-stdin"))
		}
		if *stdin {
			bytes, err := io.ReadAll(io.LimitReader(os.Stdin, 4097))
			if err != nil {
				return printErr("Could not read payload", err)
			}
			payload = bytes
		} else {
			payload = []byte(*data)
		}
	}
	if len(payload) > 4096 {
		return printErr("Invalid edit", fmt.Errorf("replacement payload is limited to 4096 bytes"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	stream := *channel
	inbox := *principal != ""
	if inbox {
		stream = *principal
	}
	response, err := client.MutateManagedRealtimeMessage(context.Background(), fs.Arg(0), fs.Arg(1), stream, inbox, *id, remove, api.ManagedRealtimeMessageMutationRequest{ExpectedVersion: *version, DataBase64: base64.StdEncoding.EncodeToString(payload), Binary: *binary})
	if err != nil {
		return printErr("Could not mutate message", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	PrintOK(osStdout, "Message %s %s at version %d, sequence %d.", response.MessageID, response.Event, response.Version, response.Sequence)
	return 0
}
