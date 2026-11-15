package main

import (
	"errors"
	"flag"
	"unicode/utf8"
)

func cmdExportDiff(args []string) int {
	fs := newFlagSet("export diff", flag.ContinueOnError)
	key := fs.String("key", "id", "unique top-level string or number field (default id)")
	explain := fs.Bool("explain", false, "show changed field paths and kinds without field values")
	failOnChange := fs.Bool("fail-on-change", false, "exit 1 when added, removed or changed records exist")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 2 || fs.Arg(0) == "" || fs.Arg(1) == "" || fs.Arg(0) == "-" || fs.Arg(1) == "-" || *key == "" || !utf8.ValidString(*key) {
		return printErr("Invalid export comparison", errors.New("use two local files and a nonempty UTF-8 --key; raw '-' is not a file input"))
	}
	report, err := diffExportFilesMode(fs.Arg(0), fs.Arg(1), *key, *explain)
	if err != nil {
		return printErr("Could not compare exports", err)
	}
	if err := writeExportDiff(osStdout, report, jsonOutput); err != nil {
		return printErr("Could not write export comparison", err)
	}
	if *failOnChange && report.HasChanges {
		return 1
	}
	return 0
}
