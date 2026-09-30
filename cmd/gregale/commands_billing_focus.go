package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/focus"
)

func cmdBillingExport(args []string) int {
	fs := newFlagSet("billing export", flag.ContinueOnError)
	month := fs.String("month", "", "invoice period-end month (YYYY-MM, required)")
	format := fs.String("format", "zip", "zip (CSV + metadata), csv, or metadata")
	output := fs.String("out", "", "write to a new file; - explicitly writes stdout")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if err := validateBillingExportFlags(*month, *format, *output, fs.NArg()); err != nil {
		return printErr("Invalid billing export options", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	data, err := client.ExportFOCUSInvoices(context.Background(), *month, *format)
	if err != nil {
		return printErr("Could not export billing invoices", err)
	}
	if *output == "" || *output == "-" {
		_, err = osStdout.Write(data)
	} else {
		err = writeFOCUSExportFile(*output, data)
	}
	if err != nil {
		return printErr("Could not write billing export", err)
	}
	_, _ = fmt.Fprintln(os.Stderr, "FOCUS 1.4 Invoice Detail projection is partial. See billing docs and export metadata for invoice source coverage and remaining gaps.")
	return 0
}

func validateBillingExportFlags(month, format, output string, positionalCount int) error {
	if positionalCount != 0 {
		return fmt.Errorf("usage: gregale billing export --month YYYY-MM --out PATH [--format zip|csv|metadata]")
	}
	if _, err := focus.ParseMonth(month); err != nil {
		return fmt.Errorf("--month: %w", err)
	}
	if !focus.ValidFormat(format) {
		return fmt.Errorf("--format must be zip, csv, or metadata")
	}
	if format == "zip" && output == "" {
		return fmt.Errorf("ZIP export requires --out PATH (or --out - to pipe it)")
	}
	return nil
}

// Refuse replacement, including symlinks. If disk writes fail, remove the
// new incomplete file; API failures never create any output file.
func writeFOCUSExportFile(path string, data []byte) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create billing export: %w", err)
	}
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return fmt.Errorf("write billing export: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("close billing export: %w", err)
	}
	return nil
}
