package main

import (
	"encoding/json"
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

func domainLifecycleCommand(key string) bool {
	switch key {
	case "domains keys", "domains stage-key", "domains verify-key", "domains activate-key", "domains retire-key", "domains reverify":
		return true
	}
	return false
}

func domainCLIEnabled(route string, getenv func(string) string) bool {
	if !strings.HasPrefix(route, internalmail.OperationsPath+"/domains/") {
		return true
	}
	if strings.Contains(route, "/keys") {
		return internalmail.RotationEnabled(getenv)
	}
	if strings.HasSuffix(route, "/reverify") {
		return internalmail.ReverificationEnabled(getenv)
	}
	return true
}

func parseDomainCommand(args []string, diagnostics io.Writer) (command, error) {
	key := args[0] + " " + args[1]
	fs := flag.NewFlagSet("mailctl "+key, flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	id := fs.String("id", "", "domain UUID")
	var keyID, after string
	var limit int
	var audit internalmail.OperatorAuditInput
	var drained bool
	switch key {
	case "domains keys":
		addPageFlags(fs, &limit, &after)
	case "domains stage-key", "domains reverify":
		addAuditFlags(fs, &audit)
	default:
		fs.StringVar(&keyID, "key-id", "", "signing key UUID")
		addAuditFlags(fs, &audit)
		if key == "domains retire-key" {
			fs.BoolVar(&drained, "confirmed-queue-drained", false, "reviewed evidence confirms no mail still needs this selector")
		}
	}
	if err := fs.Parse(args[2:]); err != nil {
		return command{}, err
	}
	if fs.NArg() != 0 || !canonicalCLIUUID(*id) {
		return command{}, fmt.Errorf("--id must be a canonical domain UUID; positional arguments are not accepted")
	}
	base := internalmail.OperationsPath + "/domains/" + *id
	if key == "domains keys" {
		if limit < 1 || limit > api.InternalMailMaxOperatorPageSize || after != "" && !canonicalCLIUUID(after) {
			return command{}, fmt.Errorf("invalid key page bounds")
		}
		query := url.Values{"limit": {strconv.Itoa(limit)}}
		if after != "" {
			query.Set("after", after)
		}
		return command{http.MethodGet, base + "/keys?" + query.Encode(), nil}, nil
	}
	if audit.Validate() != nil {
		return command{}, fmt.Errorf("valid --request-id, --actor, --reason and --evidence are required")
	}
	if key == "domains reverify" {
		return command{http.MethodPost, base + "/reverify", audit}, nil
	}
	if key == "domains stage-key" {
		return command{http.MethodPost, base + "/keys/stage", audit}, nil
	}
	if !canonicalCLIUUID(keyID) {
		return command{}, fmt.Errorf("--key-id must be a canonical UUID")
	}
	base += "/keys/" + keyID
	switch key {
	case "domains verify-key":
		return command{http.MethodPost, base + "/verify", audit}, nil
	case "domains activate-key":
		return command{http.MethodPost, base + "/activate", audit}, nil
	case "domains retire-key":
		if !drained {
			return command{}, fmt.Errorf("--confirmed-queue-drained and reviewed evidence are required")
		}
		return command{http.MethodPost, base + "/retire", struct {
			internalmail.OperatorAuditInput
			ConfirmedQueueDrained bool `json:"confirmed_queue_drained"`
		}{audit, true}}, nil
	}
	return command{}, fmt.Errorf("unknown domain command")
}

func canonicalCLIUUID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}

func domainObservationFailed(route string, raw json.RawMessage) bool {
	activation := strings.HasSuffix(route, "/activate")
	if !strings.HasSuffix(route, "/verify") && !strings.HasSuffix(route, "/reverify") && !activation {
		return false
	}
	var record struct {
		Result struct {
			CheckResult string `json:"check_result"`
			State       string `json:"state"`
		} `json:"result"`
	}
	return json.Unmarshal(raw, &record) != nil || record.Result.CheckResult != "valid" || activation && record.Result.State != "active"
}
