package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// Canonicalize both live values and desired patches through the same decoder.
// Empty reliability and null reliability both clear overrides; caller policy
// nulls remain distinct from empty grants.
func canonicalServicePolicy(config serviceBindingConfiguration) (map[string]json.RawMessage, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for key, value := range fields {
		var normalized any
		if err := json.Unmarshal(value, &normalized); err != nil {
			return nil, err
		}
		if key == "service_reliability" && normalized == nil {
			normalized = map[string]any{}
		}
		fields[key], err = json.Marshal(normalized)
		if err != nil {
			return nil, err
		}
	}
	return fields, nil
}

func liveServicePolicy(app api.AppResponse) (serviceBindingConfiguration, error) {
	values := serviceBindingConfigurationValues(app)
	// A present empty policy denies all even if its underlying Go collection
	// is nil. Encode it as an empty collection, never an unrestricted null.
	if app.AllowedServiceCallers != nil && len(*app.AllowedServiceCallers) == 0 {
		values["allowed_service_callers"] = []string{}
	}
	if app.AllowedServiceCallScopes != nil && len(*app.AllowedServiceCallScopes) == 0 {
		values["allowed_service_call_scopes"] = api.ServiceCallerScopes{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return serviceBindingConfiguration{}, err
	}
	return decodeServiceBindingConfiguration(raw, app.Slug)
}

func cmdBindingsServicePolicy(args []string, export bool) int {
	// Exit 1 is exclusively drift; usage, I/O and API errors use 2.
	fail := servicePolicyCommandError
	name := "check"
	if export {
		name = "export"
	}
	usageError := func() int {
		return fail("Invalid service policy command", fmt.Errorf("usage: gregale bindings service %s APP %s", name, map[bool]string{true: "[--out policy.json]", false: "--file policy.json"}[export]))
	}
	flags, pos := splitArgsForFlags(args)
	if len(pos) != 1 || !api.ValidAppSlug(pos[0]) {
		return usageError()
	}
	fs := newFlagSet("bindings service "+name, flag.ContinueOnError)
	// Report parse errors through the same command-specific exit contract.
	fs.SetOutput(io.Discard)
	var path *string
	if export {
		path = fs.String("out", "", "write policy to a new private file (default stdout)")
	} else {
		path = fs.String("file", "", "JSON service binding settings policy")
	}
	if fs.Parse(flags) != nil || fs.NArg() != 0 || (!export && *path == "") {
		return usageError()
	}
	var desired serviceBindingConfiguration
	if !export {
		f, err := openCustomerFile(*path)
		if err != nil {
			return fail("Could not read policy", err)
		}
		raw, err := io.ReadAll(io.LimitReader(f, api.SourceManifestMaxBytes+1))
		_ = f.Close()
		if err != nil {
			return fail("Could not read policy", err)
		}
		if len(raw) > api.SourceManifestMaxBytes {
			return fail("Invalid policy", fmt.Errorf("policy exceeds %d bytes", api.SourceManifestMaxBytes))
		}
		desired, err = decodeServiceBindingConfiguration(raw, pos[0])
		if err != nil {
			return fail("Invalid policy", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return fail("Not logged in", err)
	}
	app, err := client.GetApp(context.Background(), pos[0])
	if err != nil {
		return fail("Could not read service configuration", err)
	}
	live, err := liveServicePolicy(app)
	if err != nil {
		return fail("Invalid live service configuration", err)
	}
	if export {
		fields, err := canonicalServicePolicy(live)
		if err != nil {
			return fail("Could not encode policy", err)
		}
		encoded, err := json.MarshalIndent(fields, "", "  ")
		if err != nil {
			return fail("Could not encode policy", err)
		}
		if len(encoded)+1 > api.SourceManifestMaxBytes {
			return fail("Could not export policy", fmt.Errorf("policy exceeds %d bytes", api.SourceManifestMaxBytes))
		}
		if *path == "" {
			if err := writeJSON(fields); err != nil {
				return fail("Could not export policy", err)
			}
			return 0
		}
		if err := writeSavedJSONPlan(*path, fields); err != nil {
			return fail("Could not export policy", err)
		}
		if jsonOutput {
			if err := writeJSON(map[string]string{"app": pos[0], "policy_path": *path}); err != nil {
				return fail("Could not write export receipt", err)
			}
			return 0
		}
		PrintOK(osStdout, "Exported service policy for %s to %s", pos[0], *path)
		return 0
	}
	if _, err := resolveServiceBindingReliability(desired, app); err != nil {
		return fail("Invalid service reliability", err)
	}
	actual, err := canonicalServicePolicy(live)
	if err != nil {
		return fail("Could not encode policy", err)
	}
	wanted, err := canonicalServicePolicy(desired)
	if err != nil {
		return fail("Could not encode policy", err)
	}
	changes := map[string]serviceBindingConfigurationChange{}
	for key, value := range wanted {
		if string(value) == string(actual[key]) {
			continue
		}
		var before, after any
		_ = json.Unmarshal(actual[key], &before)
		_ = json.Unmarshal(value, &after)
		changes[key] = serviceBindingConfigurationChange{Before: before, After: after}
	}
	match := len(changes) == 0
	if jsonOutput {
		if err := writeJSON(struct {
			App     string                                       `json:"app"`
			Match   bool                                         `json:"match"`
			Changes map[string]serviceBindingConfigurationChange `json:"changes"`
		}{pos[0], match, changes}); err != nil {
			return fail("Could not write check", err)
		}
	} else {
		if match {
			PrintOK(osStdout, "Service policy matches %s", pos[0])
		} else {
			keys := make([]string, 0, len(changes))
			for key := range changes {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				change := changes[key]
				before, _ := json.Marshal(change.Before)
				after, _ := json.Marshal(change.After)
				_, _ = fmt.Fprintf(osStdout, "%s: live=%s desired=%s\n", key, before, after)
			}
		}
	}
	if !match {
		return 1
	}
	return 0
}

// This command reserves 1 for drift, so its JSON diagnostics must also report
// the command-specific error exit rather than the CLI's status-derived exit.
func servicePolicyCommandError(title string, err error) int {
	if !jsonOutput {
		printErr(title, err)
		return 2
	}
	problem := api.Problem{Status: 400, Code: "invalid_request", Title: title, Detail: redactErrorText(err.Error())}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		problem = apiErr.Problem
	} else if isTransportError(err) {
		problem = transportProblem(err)
	}
	_ = json.NewEncoder(osStderr).Encode(struct {
		api.Problem
		Category string `json:"category"`
		ExitCode int    `json:"exit_code"`
	}{diagnosticProblem(problem), "service_policy_error", 2})
	return 2
}
