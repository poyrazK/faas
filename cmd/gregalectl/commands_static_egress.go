package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// dispatchStaticEgress manages the static egress address inventory
// (ADR-20261128090000): additional public IPv4 addresses the provider routes
// to one compute node each, assigned to the accounts whose Scale apps may pin
// them.
const dispatchStaticEgress = "static-egress-ips"

const staticEgressAdminPath = "/v1/admin/ops/static-egress-ips"

func cmdStaticEgressDispatch(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl static-egress-ips: missing subcommand; want list|set|remove")
		return 2
	}
	switch args[0] {
	case "list":
		return cmdStaticEgressList(args[1:])
	case "set":
		return cmdStaticEgressSet(args[1:])
	case "remove":
		return cmdStaticEgressRemove(args[1:])
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl static-egress-ips: unknown subcommand %q\n", args[0])
		return 2
	}
}

func cmdStaticEgressList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl static-egress-ips list: positional arguments are not accepted")
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl static-egress-ips list: %v\n", err)
		return 1
	}
	var response api.ObsStaticEgressAddressList
	if err := newOperatorHTTPClient(&sess).doJSON(context.Background(), http.MethodGet, staticEgressAdminPath, nil, &response, false, nil); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl static-egress-ips list: %v\n", err)
		return 1
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	if len(response.Addresses) == 0 {
		_, _ = fmt.Fprintln(osStdout, "no static egress addresses")
	}
	for _, a := range response.Addresses {
		printStaticEgressAddress(a)
	}
	return 0
}

// cmdStaticEgressSet adds an address or replaces its node, account and note.
// Without --account-id the address is (or becomes) unassigned.
func cmdStaticEgressSet(args []string) int {
	fs := flag.NewFlagSet("set", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	ip := fs.String("ip", "", "the public IPv4 address")
	node := fs.String("node", "", "compute node name the provider routes the address to")
	account := fs.String("account-id", "", "account UUID the address is assigned to; omit to leave it unassigned")
	note := fs.String("note", "", "free text, e.g. the provider's order reference (at most 200 bytes)")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	addr, ok := staticEgressIPArg(fs, "set", *ip)
	if !ok {
		return 2
	}
	req := api.ObsPutStaticEgressAddressRequest{Node: strings.TrimSpace(*node), AccountID: strings.TrimSpace(*account), Note: *note}
	if req.Node == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl static-egress-ips set: --node is required")
		return 2
	}
	if req.AccountID != "" {
		if _, err := uuid.Parse(req.AccountID); err != nil {
			_, _ = fmt.Fprintln(osStderr, "gregalectl static-egress-ips set: --account-id must be a UUID")
			return 2
		}
	}
	if len(req.Note) > 200 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl static-egress-ips set: --note is at most 200 bytes")
		return 2
	}
	traceID, ok := staticEgressTraceID("set", *traceIDFlag)
	if !ok {
		return 2
	}
	var response api.ObsStaticEgressAddress
	if code := staticEgressMutate("set", http.MethodPut, staticEgressAdminPath+"/"+url.PathEscape(addr.String()), req, &response, traceID); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	printStaticEgressAddress(response)
	_, _ = fmt.Fprintf(osStdout, "trace_id=%s\n", traceID)
	return 0
}

func cmdStaticEgressRemove(args []string) int {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	ip := fs.String("ip", "", "the public IPv4 address")
	ack := fs.Bool("yes", false, "acknowledge the removal")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	addr, ok := staticEgressIPArg(fs, "remove", *ip)
	if !ok {
		return 2
	}
	if !*ack {
		_, _ = fmt.Fprintln(osStderr, "gregalectl static-egress-ips remove: --yes is required")
		return 2
	}
	traceID, ok := staticEgressTraceID("remove", *traceIDFlag)
	if !ok {
		return 2
	}
	path := staticEgressAdminPath + "/" + url.PathEscape(addr.String()) + "?confirm=true"
	if code := staticEgressMutate("remove", http.MethodDelete, path, nil, nil, traceID); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(map[string]string{"ip": addr.String(), "removed": "true", "trace_id": traceID})
	}
	_, _ = fmt.Fprintf(osStdout, "ip=%s removed=true trace_id=%s\n", addr, traceID)
	return 0
}

func staticEgressMutate(action, method, path string, input, output any, traceID string) int {
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl static-egress-ips %s: %v\n", action, err)
		return 1
	}
	headers := make(http.Header)
	headers.Set(operatorTraceIDHeader, traceID)
	if err := newOperatorHTTPClient(&sess).doJSONWithHeaders(context.Background(), method, path, input, output, true, nil, headers); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl static-egress-ips %s: %v\n", action, err)
		return 1
	}
	return 0
}

func staticEgressIPArg(fs *flag.FlagSet, action, raw string) (netip.Addr, bool) {
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl static-egress-ips %s: positional arguments are not accepted\n", action)
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil || !addr.Is4() {
		_, _ = fmt.Fprintf(osStderr, "gregalectl static-egress-ips %s: --ip must be a dotted-quad IPv4 address\n", action)
		return netip.Addr{}, false
	}
	if err := api.ValidateStaticEgressIP(addr); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl static-egress-ips %s: --ip %v\n", action, err)
		return netip.Addr{}, false
	}
	return addr, true
}

func staticEgressTraceID(action, raw string) (string, bool) {
	traceID := strings.TrimSpace(raw)
	if traceID == "" {
		traceID = wire.NewTraceID()
	}
	if !configTraceIDShape.MatchString(traceID) {
		_, _ = fmt.Fprintf(osStderr, "gregalectl static-egress-ips %s: --trace-id must be 32 lowercase hex characters\n", action)
		return "", false
	}
	return traceID, true
}

func printStaticEgressAddress(a api.ObsStaticEgressAddress) {
	account, pinned := a.AccountID, a.PinnedAppID
	if account == "" {
		account = "-"
	}
	if pinned == "" {
		pinned = "-"
	}
	_, _ = fmt.Fprintf(osStdout, "ip=%s node=%s account_id=%s pinned_app_id=%s note=%q\n", a.IP, a.Node, account, pinned, a.Note)
}
