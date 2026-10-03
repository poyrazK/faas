package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
)

func cmdCellsDispatch(args []string) int {
	if len(args) == 1 && args[0] == "list" {
		return cmdCellsList()
	}
	if len(args) == 0 || args[0] != "set" {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl cells list | set --cell-id UUID --region ID --api-base HTTPS_ORIGIN --enabled=true|false")
		return 2
	}
	fs := flag.NewFlagSet("cells set", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	id := fs.String("cell-id", "", "immutable regional cell UUID")
	region := fs.String("region", "", "immutable canonical region")
	origin := fs.String("api-base", "", "registered HTTPS API origin")
	enabled := fs.Bool("enabled", false, "explicit attachment and resolution policy (required)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "enabled" {
			explicit = true
		}
	})
	cell := globaldirectory.Cell{ID: *id, Region: *region, APIBase: *origin}
	if fs.NArg() != 0 || !explicit || cell.Validate() != nil {
		_, _ = fmt.Fprintln(osStderr, "a canonical cell UUID, region, HTTPS API origin and an explicit enabled boolean are required")
		return 2
	}
	client, err := statusOperatorClient()
	if err != nil {
		return printErr("Load operator session", err)
	}
	var out api.GlobalCellResponse
	req := api.PutGlobalCellRequest{Region: *region, APIBase: *origin, Enabled: enabled}
	if err := client.doJSON(context.Background(), http.MethodPut, "/v1/global-cells/"+url.PathEscape(*id), req, &out, true, nil); err != nil {
		return printErr("Register regional cell", err)
	}
	if jsonEnabled() {
		return emitOperatorJSON(out)
	}
	printDirectoryCell(out)
	return 0
}

func cmdCellsList() int {
	client, err := statusOperatorClient()
	if err != nil {
		return printErr("Load operator session", err)
	}
	var cells []api.GlobalCellResponse
	if err := client.doJSON(context.Background(), http.MethodGet, "/v1/global-cells", nil, &cells, false, nil); err != nil {
		return printErr("List regional cells", err)
	}
	if jsonEnabled() {
		return emitOperatorJSON(cells)
	}
	for _, cell := range cells {
		printDirectoryCell(cell)
	}
	return 0
}

func printDirectoryCell(cell api.GlobalCellResponse) {
	_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\tenabled=%t\tgeneration=%d\n", cell.Region, cell.ID, cell.APIBase, cell.Enabled, cell.Generation)
}
