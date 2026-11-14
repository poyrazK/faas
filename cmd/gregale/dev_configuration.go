package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Key coverage is a plan, not proof of delivery or of existing secret values.
type devConfiguration struct {
	Status                   string   `json:"status"`
	Required                 []string `json:"required"`
	Provided                 []string `json:"provided"`
	Planned                  []string `json:"planned"`
	ExistingValuesUnverified []string `json:"existing_values_unverified"`
	Empty                    []string `json:"empty"`
	Missing                  []string `json:"missing"`
	Unverified               []string `json:"unverified"`
}

type devConfigurationInput struct {
	required, sealed []string
	pairs            []secretsPair
	planned          map[string]bool
	files            [2]devConfigurationFile
}

type devConfigurationFile struct {
	pairs       []secretsPair
	fingerprint [sha256.Size]byte
}

func loadDevConfiguration(source, envFile, overrides string, postgres bool, buckets []api.DevBucketRequest) (devConfigurationInput, error) {
	required, sealed, err := startRequiredConfiguration(source)
	input := devConfigurationInput{required: required, sealed: sealed, planned: map[string]bool{}}
	if err != nil {
		return input, err
	}
	for index, file := range []string{envFile, overrides} {
		if file == "" {
			continue
		}
		read := readDevEnvFile
		if index == 1 {
			read = readDevServiceOverrideFile
		}
		pairs, _, err := read(file)
		if err != nil {
			return input, err
		}
		input.pairs = append(input.pairs, pairs...)
		hash := sha256.New()
		for _, pair := range pairs {
			_, _ = fmt.Fprintf(hash, "%d:%s%d:%s", len(pair.Key), pair.Key, len(pair.Value), pair.Value)
		}
		input.files[index].pairs = pairs
		copy(input.files[index].fingerprint[:], hash.Sum(nil))
	}
	if postgres {
		input.planned["DATABASE_URL"] = true
	}
	for _, bucket := range buckets {
		prefix := bucket.Prefix
		if prefix == "" {
			prefix = api.DefaultObjectStorageBindingPrefix(bucket.Name)
		}
		for _, suffix := range []string{"_ENDPOINT", "_REGION", "_BUCKET", "_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY", "_ADDRESSING_STYLE"} {
			input.planned[prefix+suffix] = true
		}
	}
	return input, nil
}

func (input devConfigurationInput) evaluate(metadata *api.AppSecretListResponse) *devConfiguration {
	r := &devConfiguration{Status: "no_requirements", Required: append([]string{}, input.required...), Provided: []string{}, Planned: []string{}, ExistingValuesUnverified: []string{}, Empty: []string{}, Missing: []string{}, Unverified: []string{}}
	var known api.AppSecretListResponse
	if metadata != nil {
		known = *metadata
	}
	coverage := evaluateEnvRequiredKeys(input.required, "source", scopeOrDefault(""), input.pairs, nil, known, devEnvFileMaxBytes)
	r.Empty = coverage.Empty
	r.ExistingValuesUnverified = coverage.ExistingValuesUnverified
	for _, key := range input.required {
		switch {
		case slices.Contains(coverage.Empty, key), slices.Contains(coverage.ExistingValuesUnverified, key):
		case !slices.Contains(coverage.Missing, key):
			r.Provided = append(r.Provided, key)
		case input.planned[key]:
			r.Planned = append(r.Planned, key)
		case metadata == nil:
			r.Unverified = append(r.Unverified, key)
		default:
			r.Missing = append(r.Missing, key)
		}
	}
	switch {
	case len(r.Empty)+len(r.Missing) > 0:
		r.Status = "needs_attention"
	case len(r.Unverified) > 0:
		r.Status = "unverified"
	case len(r.Planned) > 0:
		r.Status = "planned"
	case len(r.ExistingValuesUnverified) > 0:
		r.Status = "names_covered"
	case len(r.Required) > 0:
		r.Status = "provided"
	}
	return r
}

func (r *devConfiguration) ready() bool {
	return len(r.Empty)+len(r.Missing)+len(r.Unverified) == 0
}

func (r *devConfiguration) doctorCheck() doctorCheck {
	check := doctorCheck{Name: "env-required", Status: "ok"}
	if len(r.Empty)+len(r.Missing) > 0 {
		check.Status, check.Code = "error", api.CodeEnvVarMissing
		check.Sources = sortedUniqueStrings(append(append([]string{}, r.Empty...), r.Missing...))
		check.Hint = "Required developer keys are missing or empty: " + strings.Join(check.Sources, ", ")
	} else if len(r.Unverified) > 0 {
		check.Status, check.Code, check.Sources = "warn", "configuration_unverified", r.Unverified
		check.Hint = "Required developer keys need verification at start: " + strings.Join(r.Unverified, ", ")
	}
	if check.Status != "ok" {
		check.Why = "Unselected .env declarations do not supply remote runtime configuration."
		check.Fix = "Supply nonempty values with --env-file, request the needed developer bindings, or configure the developer app and retry."
	}
	return check
}

func (r *devConfiguration) render(out io.Writer) {
	labels := map[string]string{
		"no_requirements": "no required keys detected",
		"provided":        "required keys supplied by selected files",
		"planned":         "required keys covered by files and planned bindings",
		"unverified":      "verification needed at start",
		"needs_attention": "missing or empty required keys",
		"names_covered":   "existing key names found; values remain unverified",
	}
	PrintProgress(out, "Required developer configuration: %s", labels[r.Status])
	for _, row := range []struct {
		label string
		keys  []string
	}{{"selected files", r.Provided}, {"planned bindings", r.Planned}, {"existing names (values unverified)", r.ExistingValuesUnverified}, {"empty values", r.Empty}, {"missing keys", r.Missing}, {"unverified keys", r.Unverified}} {
		if len(row.keys) > 0 {
			PrintProgress(out, "%s: %s", row.label, strings.Join(row.keys, ", "))
		}
	}
}

// Only this session's default scope is inspected. A linked production app or
// environment must never satisfy an isolated developer session's requirements.
func verifyDevConfiguration(ctx context.Context, client *Client, session api.DevSessionResponse, input devConfigurationInput) (*devConfiguration, error) {
	input.planned = map[string]bool{}
	if session.Postgres != nil && session.Postgres.BindingID != "" {
		input.planned[session.Postgres.EnvironmentKey] = true
	}
	for _, bucket := range session.Buckets {
		if bucket.Binding.ID == "" {
			continue
		}
		keys := bucket.Binding.SecretKeys
		for _, key := range []string{keys.Endpoint, keys.Region, keys.Bucket, keys.AccessKeyID, keys.SecretAccessKey, keys.AddressingStyle} {
			if key != "" {
				input.planned[key] = true
			}
		}
	}
	r := input.evaluate(nil)
	if len(r.Unverified) == 0 {
		return r, nil
	}
	if session.App.Slug == "" {
		return r, fmt.Errorf("the developer session did not identify an app; retry with a current control plane")
	}
	ctx, cancel := context.WithTimeout(ctx, doctorAppEnvTimeout)
	defer cancel()
	metadata, err := client.ListSecretsWithScope(ctx, session.App.Slug, scopeOrDefault(""))
	if err != nil {
		return r, fmt.Errorf("could not verify developer secret names; retry or select a complete --env-file")
	}
	env, err := client.GetAppsSlugEnvWithScope(ctx, session.App.Slug, scopeOrDefault(""))
	if err != nil {
		return r, fmt.Errorf("could not verify developer environment names; retry or select a complete --env-file")
	}
	for _, key := range env.Env {
		if !slices.Contains(input.sealed, key.Key) {
			metadata.Secrets = append(metadata.Secrets, api.AppSecretResponse{Key: key.Key, Scope: scopeOrDefault("")})
		}
	}
	return input.evaluate(&metadata), nil
}

func checkDevConfiguration(ctx context.Context, client *Client, session api.DevSessionResponse, source, envFile, overrides string) (devConfigurationInput, int) {
	input, err := loadDevConfiguration(source, envFile, overrides, false, nil)
	if err != nil {
		return input, printErr("Invalid developer configuration", err)
	}
	r, verifyErr := verifyDevConfiguration(ctx, client, session, input)
	if jsonOutput {
		if err := writeNDJSON([]any{struct {
			Type string `json:"type"`
			*devConfiguration
		}{"developer_configuration", r}}); err != nil {
			return input, jsonOut(err)
		}
	} else {
		r.render(osStdout)
	}
	if verifyErr != nil || !r.ready() {
		code := api.CodeEnvVarMissing
		check := r.doctorCheck()
		detail := check.Hint
		if verifyErr != nil {
			code, detail = "configuration_unverified", verifyErr.Error()
		}
		return input, printErr("Developer configuration needs attention", &api.APIError{Problem: *api.NewProblem(422, code, "Developer configuration needs attention", detail)})
	}
	return input, 0
}
