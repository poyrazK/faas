package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/nodeclaim"
)

type nodeClaimReport struct {
	File          string           `json:"file"`
	Valid         bool             `json:"valid"`
	Node          string           `json:"node,omitempty"`
	SSHHost       string           `json:"ssh_host,omitempty"`
	SSHUser       string           `json:"ssh_user,omitempty"`
	SSHPort       int              `json:"ssh_port,omitempty"`
	HostKeySHA256 string           `json:"host_key_sha256,omitempty"`
	StorageDevice string           `json:"storage_device,omitempty"`
	FormatStorage bool             `json:"format_storage,omitempty"`
	ManifestNode  bool             `json:"manifest_node,omitempty"`
	Errors        nodeclaim.Errors `json:"errors,omitempty"`
	LoadError     string           `json:"load_error,omitempty"`
}

func cmdDeployClaim(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "gregalectl deploy claim: missing subcommand; want validate")
		return 2
	}
	switch args[0] {
	case "validate":
		return cmdDeployClaimValidate(args[1:])
	case flagHelpShort, flagHelpLong:
		fmt.Fprintln(os.Stderr, "usage: gregalectl deploy claim validate --file PATH [--manifest-file PATH]")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "gregalectl deploy claim: unknown subcommand %q (expected: validate)\n", args[0])
		return 2
	}
}

func cmdDeployClaimValidate(args []string) int {
	fs := flag.NewFlagSet("deploy claim validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	filePath := fs.String("file", "", "path to the ComputeNodeClaim YAML/JSON file (required)")
	manifestPath := fs.String("manifest-file", "", "optional signed production manifest to check for the node")
	jsonOut := fs.Bool("json", false, "emit JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *filePath == "" {
		fmt.Fprintln(os.Stderr, "gregalectl deploy claim validate: --file is required")
		return 2
	}

	claim, err := nodeclaim.Load(*filePath)
	if err != nil {
		report := nodeClaimReport{File: *filePath, LoadError: err.Error()}
		if *jsonOut || jsonOutput {
			jsonEmit(os.Stdout, report)
		} else {
			fmt.Fprintf(os.Stderr, "gregalectl deploy claim validate: %v\n", err)
		}
		return 3
	}

	errs := claim.Validate()
	report := nodeClaimReport{File: *filePath}
	if len(errs) == 0 {
		n := claim.Normalize()
		report.Node = n.Name
		report.SSHHost = n.SSHHost
		report.SSHUser = n.SSHUser
		report.SSHPort = n.SSHPort
		report.HostKeySHA256 = n.HostKeySHA256
		report.StorageDevice = n.StorageDevice
		report.FormatStorage = n.FormatStorage
	}
	if *manifestPath != "" && len(errs) == 0 {
		m, manifestErr := manifest.Load(*manifestPath)
		if manifestErr != nil {
			report.LoadError = manifestErr.Error()
			if *jsonOut || jsonOutput {
				jsonEmit(os.Stdout, report)
			} else {
				fmt.Fprintf(os.Stderr, "gregalectl deploy claim validate: %v\n", manifestErr)
			}
			return 3
		}
		if manifestErrs := m.Validate(); manifestErrs != nil {
			report.LoadError = fmt.Sprintf("invalid production manifest: %v", manifestErrs)
			if *jsonOut || jsonOutput {
				jsonEmit(os.Stdout, report)
			} else {
				fmt.Fprintf(os.Stderr, "gregalectl deploy claim validate: %s\n", report.LoadError)
			}
			return 3
		}
		for _, host := range m.Fleet.Hosts {
			if host.Name == report.Node {
				report.ManifestNode = true
				if host.Role != roleComputeOnly {
					errs = append(errs, nodeclaim.Error{Path: "metadata.name", Message: fmt.Sprintf("manifest node %q has role %q; a ComputeNodeClaim requires compute-only", report.Node, host.Role)})
				}
				break
			}
		}
		if !report.ManifestNode {
			if _, dynamicErr := m.DynamicComputeHost(report.Node, report.StorageDevice); dynamicErr != nil {
				errs = append(errs, nodeclaim.Error{Path: "metadata.name", Message: fmt.Sprintf("production manifest does not authorize dynamic node %q: %v", report.Node, dynamicErr)})
			} else if m.Fleet.ComputeNodeCount()+1 > m.Fleet.DynamicCompute.MaxNodes {
				errs = append(errs, nodeclaim.Error{Path: "metadata.name", Message: fmt.Sprintf("dynamic compute policy allows at most %d compute nodes", m.Fleet.DynamicCompute.MaxNodes)})
			} else {
				report.ManifestNode = true
			}
		}
	}
	report.Errors = errs
	report.Valid = len(errs) == 0 && report.LoadError == ""
	if *jsonOut || jsonOutput {
		jsonEmit(os.Stdout, report)
	} else if report.Valid {
		_, _ = fmt.Fprintf(os.Stdout, "valid ComputeNodeClaim node=%s ssh=%s:%d\n", report.Node, report.SSHHost, report.SSHPort)
	} else {
		for _, validationErr := range errs {
			fmt.Fprintf(os.Stderr, "gregalectl deploy claim validate: %s\n", validationErr)
		}
	}
	if !report.Valid {
		return 1
	}
	return 0
}
