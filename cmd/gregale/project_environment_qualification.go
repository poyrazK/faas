package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

const (
	defaultQualificationProbeTimeout = 5
	maxQualificationProbeTimeout     = 30
	maxQualificationPathBytes        = 2048
	qualificationProbeBodyDrainBytes = 4096
)

type projectEnvironmentQualificationClient interface {
	GetProjectEnvironmentState(context.Context, string, string) (api.ProjectEnvironmentStateResponse, error)
	GetDeploymentURL(context.Context, string) (api.DeploymentPreviewURL, error)
	CreateProjectEnvironmentQualification(context.Context, string, string, api.CreateProjectEnvironmentQualificationRequest) (api.ProjectEnvironmentQualificationResponse, error)
}

type projectEnvironmentQualificationProfile struct {
	Version        int                                                `yaml:"version"`
	TimeoutSeconds int                                                `yaml:"timeout_seconds"`
	Workloads      map[string]projectEnvironmentQualificationWorkload `yaml:"workloads"`
}

type projectEnvironmentQualificationWorkload struct {
	HealthPath string `yaml:"health_path"`
	SmokePath  string `yaml:"smoke_path"`
}

func readProjectEnvironmentQualificationProfile(file string) (projectEnvironmentQualificationProfile, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return projectEnvironmentQualificationProfile{}, fmt.Errorf("read qualification profile: %w", err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	var profile projectEnvironmentQualificationProfile
	if err := decoder.Decode(&profile); err != nil {
		return projectEnvironmentQualificationProfile{}, fmt.Errorf("decode qualification profile: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return projectEnvironmentQualificationProfile{}, errors.New("qualification profile must contain one YAML document")
		}
		return projectEnvironmentQualificationProfile{}, fmt.Errorf("decode qualification profile: %w", err)
	}
	if profile.Version != 1 {
		return projectEnvironmentQualificationProfile{}, errors.New("qualification profile version must be 1")
	}
	if profile.TimeoutSeconds == 0 {
		profile.TimeoutSeconds = defaultQualificationProbeTimeout
	}
	if profile.TimeoutSeconds < 1 || profile.TimeoutSeconds > maxQualificationProbeTimeout {
		return projectEnvironmentQualificationProfile{}, fmt.Errorf("timeout_seconds must be between 1 and %d", maxQualificationProbeTimeout)
	}
	if len(profile.Workloads) == 0 {
		return projectEnvironmentQualificationProfile{}, errors.New("qualification profile must define at least one workload")
	}
	for slug, workload := range profile.Workloads {
		if !api.ValidProjectSlug(slug) {
			return projectEnvironmentQualificationProfile{}, fmt.Errorf("invalid workload slug %q in qualification profile", slug)
		}
		if err := validateProjectEnvironmentQualificationPath(workload.HealthPath); err != nil {
			return projectEnvironmentQualificationProfile{}, fmt.Errorf("workload %q health_path: %w", slug, err)
		}
		if err := validateProjectEnvironmentQualificationPath(workload.SmokePath); err != nil {
			return projectEnvironmentQualificationProfile{}, fmt.Errorf("workload %q smoke_path: %w", slug, err)
		}
	}
	return profile, nil
}

func validateProjectEnvironmentQualificationPath(value string) error {
	if value == "" || len(value) > maxQualificationPathBytes || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") ||
		strings.ContainsAny(value, "?#\\\r\n\x00") {
		return errors.New("must be a root-relative path without a query, fragment, or control character")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return errors.New("must not contain dot segments")
		}
	}
	return nil
}

func qualifyProjectEnvironmentWithProfile(ctx context.Context, client projectEnvironmentQualificationClient, projectSlug, environmentSlug, profilePath string) (api.ProjectEnvironmentQualificationResponse, error) {
	profile, err := readProjectEnvironmentQualificationProfile(profilePath)
	if err != nil {
		return api.ProjectEnvironmentQualificationResponse{}, err
	}
	return qualifyProjectEnvironmentWithProfileAndHTTPClient(ctx, client, projectSlug, environmentSlug, profile, nil)
}

func qualifyProjectEnvironmentWithProfileAndHTTPClient(ctx context.Context, client projectEnvironmentQualificationClient, projectSlug, environmentSlug string, profile projectEnvironmentQualificationProfile, probeClient *http.Client) (api.ProjectEnvironmentQualificationResponse, error) {
	snapshot, err := client.GetProjectEnvironmentState(ctx, projectSlug, environmentSlug)
	if err != nil {
		return api.ProjectEnvironmentQualificationResponse{}, fmt.Errorf("read active environment release: %w", err)
	}
	releaseSet := snapshot.ActiveReleaseSet
	if releaseSet == nil || !releaseSet.Active || snapshot.ReleaseSetStatus != "active" ||
		snapshot.ProjectSlug != projectSlug || snapshot.Environment != environmentSlug || releaseSet.Environment != environmentSlug {
		return api.ProjectEnvironmentQualificationResponse{}, errors.New("environment has no active release set to qualify")
	}
	configuration := snapshot.Configuration
	if configuration.ProjectSlug != projectSlug || configuration.Environment != environmentSlug || configuration.Version < 0 ||
		!api.ValidProjectEnvironmentConfigHash(configuration.ConfigHash) {
		return api.ProjectEnvironmentQualificationResponse{}, errors.New("environment state does not include a valid configuration identity; retry qualification")
	}
	if len(releaseSet.Members) == 0 {
		return api.ProjectEnvironmentQualificationResponse{}, errors.New("active release set has no workloads")
	}

	deploymentByApp := make(map[string]string, len(releaseSet.Members))
	for _, member := range releaseSet.Members {
		if member.AppID == "" || member.DeploymentID == "" {
			return api.ProjectEnvironmentQualificationResponse{}, errors.New("active release set contains an incomplete workload identity")
		}
		if _, exists := deploymentByApp[member.AppID]; exists {
			return api.ProjectEnvironmentQualificationResponse{}, errors.New("active release set contains duplicate workload identities")
		}
		deploymentByApp[member.AppID] = member.DeploymentID
	}
	workloadBySlug := make(map[string]api.ProjectEnvironmentStateWorkloadResponse, len(releaseSet.Members))
	for _, workload := range snapshot.Workloads {
		deploymentID, member := deploymentByApp[workload.AppID]
		if !member {
			continue
		}
		if workload.WorkloadSlug == "" || workload.Release.DeploymentID != deploymentID {
			return api.ProjectEnvironmentQualificationResponse{}, errors.New("environment state does not match the active release-set members; retry qualification")
		}
		if _, exists := workloadBySlug[workload.WorkloadSlug]; exists {
			return api.ProjectEnvironmentQualificationResponse{}, errors.New("environment state contains duplicate workload slugs")
		}
		workloadBySlug[workload.WorkloadSlug] = workload
	}
	if len(workloadBySlug) != len(deploymentByApp) {
		return api.ProjectEnvironmentQualificationResponse{}, errors.New("environment state is missing an active release-set workload")
	}
	if len(profile.Workloads) != len(workloadBySlug) {
		return api.ProjectEnvironmentQualificationResponse{}, errors.New("qualification profile must define exactly the active release-set workloads")
	}
	for slug := range workloadBySlug {
		if _, exists := profile.Workloads[slug]; !exists {
			return api.ProjectEnvironmentQualificationResponse{}, fmt.Errorf("qualification profile is missing workload %q", slug)
		}
	}
	for slug := range profile.Workloads {
		if _, exists := workloadBySlug[slug]; !exists {
			return api.ProjectEnvironmentQualificationResponse{}, fmt.Errorf("qualification profile workload %q is not in the active release set", slug)
		}
	}
	secretRevisionHashes, err := projectEnvironmentQualificationSecretRevisionHashes(workloadBySlug)
	if err != nil {
		return api.ProjectEnvironmentQualificationResponse{}, fmt.Errorf("fingerprint environment secret revisions: %w", err)
	}

	slugs := make([]string, 0, len(workloadBySlug))
	for slug := range workloadBySlug {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	clientHTTP := &http.Client{}
	if probeClient != nil {
		*clientHTTP = *probeClient
	}
	clientHTTP.Timeout = time.Duration(profile.TimeoutSeconds) * time.Second
	clientHTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	urls := make(map[string]string, len(slugs))
	for _, slug := range slugs {
		workload := workloadBySlug[slug]
		if workload.Release.Status != "live" {
			continue
		}
		preview, err := client.GetDeploymentURL(ctx, workload.Release.DeploymentID)
		if err != nil {
			return api.ProjectEnvironmentQualificationResponse{}, fmt.Errorf("resolve exact preview URL for workload %q: %w", slug, err)
		}
		if preview.DeploymentID != workload.Release.DeploymentID || !preview.Alive || preview.URL == "" || !validQualificationPreviewBase(preview.URL) {
			continue
		}
		urls[slug] = preview.URL
	}

	checks := make([]api.ProjectEnvironmentQualificationCheck, 0, 2)
	for _, checkName := range []string{"health", "smoke"} {
		results := make([]api.ProjectEnvironmentQualificationResult, 0, len(slugs))
		for _, slug := range slugs {
			workload := workloadBySlug[slug]
			result := api.ProjectEnvironmentQualificationResult{
				WorkloadSlug: slug, DeploymentID: workload.Release.DeploymentID,
				Status: "failed", ErrorCode: "preview_unavailable",
			}
			if baseURL := urls[slug]; baseURL != "" {
				probePath := profile.Workloads[slug].HealthPath
				if checkName == "smoke" {
					probePath = profile.Workloads[slug].SmokePath
				}
				probeResult := runProjectEnvironmentQualificationProbe(ctx, clientHTTP, baseURL, probePath)
				result = api.ProjectEnvironmentQualificationResult{
					WorkloadSlug: slug, DeploymentID: workload.Release.DeploymentID,
					Status: probeResult.Status, HTTPStatus: probeResult.HTTPStatus, ErrorCode: probeResult.ErrorCode,
				}
			}
			results = append(results, result)
		}
		status := "passed"
		for _, result := range results {
			if result.Status != "passed" {
				status = "failed"
				break
			}
		}
		checks = append(checks, api.ProjectEnvironmentQualificationCheck{Name: checkName, Status: status, Results: results})
	}
	return client.CreateProjectEnvironmentQualification(ctx, projectSlug, environmentSlug,
		api.CreateProjectEnvironmentQualificationRequest{
			ReleaseSetID: releaseSet.ID, ConfigurationVersion: configuration.Version,
			ConfigurationHash: configuration.ConfigHash, SecretRevisionHashes: secretRevisionHashes, Checks: checks,
		})
}

func projectEnvironmentQualificationSecretRevisionHashes(workloads map[string]api.ProjectEnvironmentStateWorkloadResponse) (map[string]string, error) {
	hashes := make(map[string]string, len(workloads))
	for slug, workload := range workloads {
		revisions := make([]api.ProjectEnvironmentSecretRevision, 0, len(workload.Secrets))
		for _, secret := range workload.Secrets {
			revisions = append(revisions, api.ProjectEnvironmentSecretRevision{
				Key: secret.Key, Version: secret.Version, ManagedBy: secret.ManagedBy,
				BindingID: secret.BindingID, CredentialGeneration: secret.CredentialGeneration,
			})
		}
		hash, err := api.ProjectEnvironmentSecretRevisionHash(revisions)
		if err != nil {
			return nil, fmt.Errorf("workload %q: %w", slug, err)
		}
		hashes[slug] = hash
	}
	return hashes, nil
}

type projectEnvironmentQualificationProbeResult struct {
	Status     string
	HTTPStatus *int
	ErrorCode  string
}

func validQualificationPreviewBase(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil &&
		(parsed.Path == "" || parsed.Path == "/") && parsed.RawQuery == "" && parsed.Fragment == ""
}

func runProjectEnvironmentQualificationProbe(ctx context.Context, client *http.Client, baseURL, probePath string) projectEnvironmentQualificationProbeResult {
	if !validQualificationPreviewBase(baseURL) || validateProjectEnvironmentQualificationPath(probePath) != nil {
		return projectEnvironmentQualificationProbeResult{Status: "failed", ErrorCode: "preview_unavailable"}
	}
	base, _ := url.Parse(baseURL)
	base.Path = probePath
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return projectEnvironmentQualificationProbeResult{Status: "failed", ErrorCode: "request_failed"}
	}
	response, err := client.Do(request)
	if err != nil {
		return projectEnvironmentQualificationProbeResult{Status: "failed", ErrorCode: "request_failed"}
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, qualificationProbeBodyDrainBytes))
	status := response.StatusCode
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return projectEnvironmentQualificationProbeResult{Status: "passed", HTTPStatus: &status}
	}
	return projectEnvironmentQualificationProbeResult{Status: "failed", HTTPStatus: &status, ErrorCode: "unexpected_status"}
}
