package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func eventFeedCLIRoute(route string) bool {
	return route == internalmail.EventFeedOperationsPath || strings.HasPrefix(route, internalmail.EventFeedOperationsPath+"/")
}
func parseEventFeedCommand(args []string, diagnostics io.Writer) (command, error) {
	fs := flag.NewFlagSet("mailctl events "+args[1], flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	var producer, consumer, cursor, after string
	var fromStart bool
	var audit internalmail.OperatorAuditInput
	generation := int64(-1)
	limit := api.InternalMailOperatorPageSize
	fs.StringVar(&producer, "producer", "", "stable producer ID")
	switch args[1] {
	case "consumers":
		addPageFlags(fs, &limit, &after)
	case "status", "replay":
		fs.StringVar(&consumer, "consumer", "", "consumer name")
		if args[1] == "replay" {
			fs.StringVar(&cursor, "after", "", "owned feed event UUID to resume after")
			fs.BoolVar(&fromStart, "from-start", false, "replay from the beginning")
			fs.Int64Var(&generation, "expected-generation", -1, "generation from events status")
			addAuditFlags(fs, &audit)
		}
	default:
		return command{}, fmt.Errorf("unknown events command; use mailctl help")
	}
	if err := fs.Parse(args[2:]); err != nil {
		return command{}, err
	}
	if fs.NArg() != 0 {
		return command{}, fmt.Errorf("positional arguments are not accepted")
	}
	if _, err := internalmail.ProducerMessagesRoute(producer); err != nil {
		return command{}, fmt.Errorf("valid --producer is required")
	}
	if args[1] == "consumers" {
		if limit < 1 || limit > api.InternalMailMaxOperatorPageSize || len(after) > api.InternalMailEventConsumerNameBytes {
			return command{}, fmt.Errorf("invalid page bounds")
		}
		query := url.Values{"limit": {strconv.Itoa(limit)}}
		if after != "" {
			if _, err := internalmail.EventConsumerRoute(producer, after); err != nil {
				return command{}, fmt.Errorf("invalid consumer cursor")
			}
			query.Set("after", after)
		}
		return command{http.MethodGet, internalmail.EventFeedOperationsPath + "/" + producer + "?" + query.Encode(), nil}, nil
	}
	input := internalmail.EventFeedReplayRequest{OperatorAuditInput: audit, ProducerID: producer, Consumer: consumer, Cursor: cursor, ExpectedGeneration: &generation}
	if _, err := internalmail.EventConsumerRoute(producer, consumer); err != nil {
		return command{}, fmt.Errorf("valid --consumer is required")
	}
	if args[1] == "status" {
		return command{http.MethodGet, internalmail.EventFeedOperationsPath + "/" + producer + "/" + consumer, nil}, nil
	}
	if fromStart == (cursor != "") || input.Validate() != nil {
		return command{}, fmt.Errorf("replay requires --from-start or --after UUID, a positive generation and all audit flags")
	}
	return command{http.MethodPost, internalmail.EventFeedOperationsPath + "/replay", input}, nil
}
