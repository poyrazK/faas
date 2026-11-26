package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/dataapi/connectchecks"
)

// prepareDataAPIConnectChecks is offline and only creates the requested new
// collection. Fixture keys remain in the private user-owned collection.
func prepareDataAPIConnectChecks(input, output string) int {
	file, err := openCustomerFile(input)
	if err != nil {
		return printErr("Cannot read access matrix", err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > connectchecks.MaxInputBytes {
		_ = file.Close()
		return printErr("Invalid access matrix", errors.New("use a regular file under 32 KiB"))
	}
	data, err := io.ReadAll(io.LimitReader(file, connectchecks.MaxInputBytes+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil || len(data) > connectchecks.MaxInputBytes {
		return printErr("Cannot read access matrix", errors.New("could not read the bounded file"))
	}
	spec, err := connectchecks.Decode(data)
	if err != nil {
		return printErr("Invalid access matrix", err)
	}
	collection, summary, err := connectchecks.Build(spec)
	if err != nil {
		return printErr("Invalid access matrix", err)
	}
	content, err := json.MarshalIndent(collection, "", "  ")
	if err != nil {
		return printErr("Cannot encode access checks", errors.New("could not encode collection"))
	}
	content = append(content, '\n')
	if len(content) > 65536 {
		return printErr("Cannot encode access checks", errors.New("collection exceeds 64 KiB"))
	}
	path, err := filepath.Abs(output)
	if err != nil {
		return printErr("Invalid collection path", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".gregale-connect-checks-*")
	if err != nil {
		return printErr("Cannot create access checks", err)
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	n, writeErr := temp.Write(content)
	if writeErr == nil && n != len(content) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = temp.Sync()
	}
	closeErr = temp.Close()
	if writeErr != nil {
		return printErr("Cannot write access checks", writeErr)
	}
	if closeErr != nil {
		return printErr("Cannot close access checks", closeErr)
	}
	if err := os.Link(temp.Name(), path); err != nil {
		return printErr("Cannot create access checks", errors.New("choose a new collection path in an existing directory"))
	}
	if jsonOutput {
		if err := writeJSON(map[string]any{"event": "connect_checks_prepared", "collection": path, "fixtures": summary.Fixtures, "allowed": summary.Allowed, "denied": summary.Denied, "authorization": "not_tested"}); err != nil {
			return printErr("Cannot write result", err)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Created %q: %d fixtures, %d visible and %d hidden expectations. Authorization has not been tested.\n", path, summary.Fixtures, summary.Allowed, summary.Denied)
		if summary.Denied == 0 {
			_, _ = fmt.Fprintln(osStdout, "This matrix has no hidden-row expectations and does not check cross-user isolation.")
		}
		_, _ = fmt.Fprintln(osStdout, "Review the collection, map the named identities to application JWT environments, and pass both files to connect --apply.")
	}
	return 0
}
