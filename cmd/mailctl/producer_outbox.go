package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func producerOutboxCLIRoute(route string) bool {
	return route == internalmail.ProducerOutboxPath || strings.HasPrefix(route, internalmail.ProducerOutboxPath+"/")
}

func outboxAdmissionCLIRoute(route string) bool {
	return strings.HasPrefix(route, internalmail.ProducerOutboxPath+"/items/") && strings.HasSuffix(route, "/admission")
}

func parseProducerOutboxCommand(args []string, diagnostics io.Writer) (command, error) {
	fs := flag.NewFlagSet("mailctl outbox "+args[1], flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	var id, after, status string
	limit := api.InternalMailOperatorPageSize
	switch args[1] {
	case "status":
	case "list":
		addPageFlags(fs, &limit, &after)
		fs.StringVar(&status, "status", "", "handoff state filter")
	case "show", "admission":
		fs.StringVar(&id, "id", "", "notification intent UUID")
	default:
		return command{}, fmt.Errorf("unknown outbox command; use mailctl help")
	}
	if err := fs.Parse(args[2:]); err != nil {
		return command{}, err
	}
	if fs.NArg() != 0 {
		return command{}, fmt.Errorf("positional arguments are not accepted")
	}
	if args[1] == "status" {
		return command{http.MethodGet, internalmail.ProducerOutboxPath, nil}, nil
	}
	if args[1] == "show" || args[1] == "admission" {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return command{}, fmt.Errorf("valid --id is required")
		}
		route := internalmail.ProducerOutboxPath + "/items/" + id
		if args[1] == "admission" {
			route += "/admission"
		}
		return command{http.MethodGet, route, nil}, nil
	}
	if limit < 1 || limit > api.InternalMailMaxOperatorPageSize {
		return command{}, fmt.Errorf("invalid page size")
	}
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if after != "" {
		parsed, err := uuid.Parse(after)
		if err != nil || parsed == uuid.Nil || parsed.String() != after {
			return command{}, fmt.Errorf("invalid page cursor")
		}
		query.Set("after", after)
	}
	if status != "" {
		query.Set("status", status)
	}
	return command{http.MethodGet, internalmail.ProducerOutboxPath + "/items?" + query.Encode(), nil}, nil
}
