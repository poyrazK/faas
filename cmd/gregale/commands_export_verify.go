package main

import (
	"errors"
	"flag"
)

func cmdExport(args []string) int {
	if len(args) > 0 && args[0] == "diff" {
		return cmdExportDiff(args[1:])
	}
	if len(args) == 0 || args[0] != "verify" {
		PrintUsage(osStderr, "usage: gregale export <verify|diff> [arguments]", "export")
		return 1
	}
	fs := newFlagSet("export verify", flag.ContinueOnError)
	receipt := fs.String("receipt", "", "saved JSON completion receipt")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 1 || fs.Arg(0) == "" || fs.Arg(0) == "-" || *receipt == "" || *receipt == "-" {
		return printErr("Invalid export verification", errors.New("use one local FILE and --receipt RECEIPT; raw '-' is not a file input"))
	}
	result, err := verifyExportFiles(fs.Arg(0), *receipt)
	if err != nil {
		return printErr("Could not verify export", err)
	}
	if err := writeExportVerification(osStdout, result, jsonOutput); err != nil {
		return printErr("Could not write export verification", err)
	}
	if !result.Verified {
		return 1
	}
	return 0
}
