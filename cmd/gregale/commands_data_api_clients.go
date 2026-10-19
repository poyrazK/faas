// adr: 20261018170742 — Python and Go clients rendered locally from the contract snapshot.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/dataapi/clientgen"
)

func renderDataAPIClient(contract dataAPIContract, language, pkg string) ([]byte, error) {
	switch language {
	case "python":
		return clientgen.RenderPython(contract)
	case "go":
		return clientgen.RenderGo(contract, pkg)
	}
	return nil, fmt.Errorf("unsupported client language %q", language)
}

// dataAPIClientTypes renders from a saved snapshot, or from the deployed app's
// version-checked snapshot task. Either way the CLI never sees credentials.
func dataAPIClientTypes(slug, language, pkg, snapshotFile, output string, check bool, timeout time.Duration) int {
	var contract dataAPIContract
	var err error
	if snapshotFile != "" {
		if contract, err = readDataAPIContractFile(snapshotFile); err != nil {
			return printErr("Could not read the schema snapshot", err)
		}
	} else {
		client, clientErr := authedClient()
		if clientErr != nil {
			return printErr("Not logged in", clientErr)
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		task, taskErr := generateDataAPIContract(ctx, client, slug)
		if taskErr != nil {
			return printErr("Schema generation failed", taskErr)
		}
		if contract, err = parseDataAPIContract([]byte(task.StdoutTail)); err != nil {
			return printErr("Schema generation failed", err)
		}
	}
	rendered, err := renderDataAPIClient(contract, language, pkg)
	if err != nil {
		return printErr("Could not render the client", err)
	}
	if output == "" {
		if check {
			return printErr("Invalid flags", errors.New("--check requires --output"))
		}
		_, err = osStdout.Write(rendered)
		if err != nil {
			return printErr("Could not write the client", err)
		}
		return 0
	}
	if err = writeDataAPITypes(output, check, string(rendered)); err != nil {
		return printErr("Could not export the client", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"language": language, "fingerprint": contract.Fingerprint, "output": output, "checked": check}))
	}
	PrintOK(osStdout, "%s client %s: %s", language, map[bool]string{true: "matches", false: "written"}[check], output)
	return 0
}
