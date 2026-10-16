package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
)

func cmdIssueSourceMaps(args []string) int {
	usage := "usage: gregale issues source-maps <publish|status|retire> --app SLUG --deployment UUID [--directory /app/dist --manifest-file FILE]"
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, usage)
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	fs := newFlagSet("issues source-maps", flag.ContinueOnError)
	app := fs.String("app", "", "application slug")
	dep := fs.String("deployment", "", "artifact-owning deployment UUID")
	directory := fs.String("directory", "", "deployed directory corresponding to the build scan root")
	file := fs.String("manifest-file", "", "portable manifest produced by the Node build command")
	flags, positionals := normalizeDebugFlagArgs(args[1:], map[string]bool{"app": true, "deployment": true, "directory": true, "manifest-file": true})
	if fs.Parse(flags) != nil {
		return 1
	}
	if *app == "" || *dep == "" || len(positionals) != 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	var request api.PublishIssueSourceMapsRequest
	if args[0] == "publish" {
		if *file == "" || *directory == "" {
			fmt.Fprintln(os.Stderr, usage)
			return 1
		}
		input, err := openCustomerFile(*file)
		if err != nil {
			return printErr("Could not read source-map manifest", err)
		}
		defer func() { _ = input.Close() }()
		data, err := io.ReadAll(io.LimitReader(input, api.IssueSourceMapsUploadMaxBytes+1))
		if err != nil || len(data) > api.IssueSourceMapsUploadMaxBytes {
			return printErr("Invalid manifest", fmt.Errorf("manifest exceeds its byte bound or could not be read"))
		}
		if err = json.Unmarshal(data, &request.Manifest); err != nil {
			return printErr("Invalid manifest", err)
		}
		request.Directory = *directory
		if _, err = issues.CompileSourceMaps(request); err != nil {
			return printErr("Invalid manifest", err)
		}
	} else if args[0] != "status" && args[0] != "retire" {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.IssueSourceMapsMetadata
	switch args[0] {
	case "publish":
		out, err = c.PublishIssueSourceMaps(context.Background(), *app, *dep, request)
	case "status":
		out, err = c.GetIssueSourceMaps(context.Background(), *app, *dep)
	case "retire":
		out, err = c.RetireIssueSourceMaps(context.Background(), *app, *dep)
	}
	if err != nil {
		return printErr("Could not manage source-map artifacts", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	state := "retired"
	if out.Ready {
		state = "ready"
	}
	_, _ = fmt.Fprintf(osStdout, "Source maps %s: %d maps for deployment %s (%s)\n", state, out.MapCount, out.DeploymentID, out.ManifestSHA256)
	return 0
}
