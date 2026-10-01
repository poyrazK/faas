package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdRegionsDispatch(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(osStderr, "usage: gregalectl regions list | set --region ID --display-name NAME --accepting-new-apps=true|false")
		return 2
	}
	if args[0] == "list" {
		if len(args) != 1 {
			return 2
		}
		client, err := statusOperatorClient()
		if err != nil {
			return printErr("Load operator session", err)
		}
		var regions []api.ComputeRegionResponse
		if err := client.doJSON(context.Background(), http.MethodGet, "/v1/regions", nil, &regions, false, nil); err != nil {
			return printErr("List regions", err)
		}
		if jsonEnabled() {
			return emitOperatorJSON(regions)
		}
		for _, r := range regions {
			fmt.Fprintf(osStdout, "%s\t%s\t%s\tnew apps=%t\t%d eligible node(s)\thealth=%s\n", r.ID, r.DisplayName, r.Availability, r.AcceptingNewApps, r.EligibleNodes, r.ObservedHealth)
		}
		return 0
	}
	if args[0] != "set" {
		fmt.Fprintf(osStderr, "unknown regions command %q\n", args[0])
		return 2
	}
	fs := flag.NewFlagSet("regions set", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	id := fs.String("region", "", "canonical region identifier")
	name := fs.String("display-name", "", "human-readable region name")
	accepting := fs.Bool("accepting-new-apps", false, "whether new apps may select this region (required)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "accepting-new-apps" {
			explicit = true
		}
	})
	if fs.NArg() != 0 || *id == "" || !api.ValidComputeRegion(*id) || !api.ValidComputeRegionDisplayName(*name) || !explicit {
		fmt.Fprintln(osStderr, "region, display-name and an explicit accepting-new-apps boolean are required")
		return 2
	}
	client, err := statusOperatorClient()
	if err != nil {
		return printErr("Load operator session", err)
	}
	var region api.ComputeRegionResponse
	req := api.UpdateComputeRegionRequest{DisplayName: *name, AcceptingNewApps: accepting}
	if err := client.doJSON(context.Background(), http.MethodPut, "/v1/regions/"+url.PathEscape(*id), req, &region, true, nil); err != nil {
		return printErr("Update region policy", err)
	}
	if jsonEnabled() {
		return emitOperatorJSON(region)
	}
	fmt.Fprintf(osStdout, "%s: accepting new apps=%t\n", region.ID, region.AcceptingNewApps)
	return 0
}
