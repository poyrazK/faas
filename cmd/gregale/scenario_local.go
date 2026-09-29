package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func validateLocalTestURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(raw, "#") || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("--base-url must be an HTTP loopback origin, such as http://localhost:3000, without a path, credentials, query, or fragment")
	}
	host := parsed.Hostname()
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", errors.New("--base-url port must be between 1 and 65535")
		}
	}
	if !strings.EqualFold(host, "localhost") {
		address := net.ParseIP(host)
		if address == nil || !address.IsLoopback() {
			return "", errors.New("--engine local accepts only localhost or a loopback IP address")
		}
	}
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func validateLocalTestScenario(scenario testScenario) error {
	if scenario.WaitFor.QueueIdle || len(scenario.WaitFor.Invocations) > 0 || len(scenario.WaitFor.Objects) > 0 || len(scenario.WaitFor.Deliveries) > 0 {
		return errors.New("--engine local cannot evaluate platform wait_for conditions; use real-vm for those checks")
	}
	return nil
}

func localTestConsumerEnv(scenario testScenario) ([]string, error) {
	env := make([]string, 0, len(scenario.Consumers)*2)
	keys := make(map[string]string, len(scenario.Consumers))
	for _, consumer := range scenario.Consumers {
		prefix := "GREGALE_TEST_CONSUMER_" + strings.ToUpper(strings.ReplaceAll(consumer.Name, "-", "_"))
		for _, suffix := range []string{"_KEY", "_ID"} {
			if value := os.Getenv(prefix + suffix); value != "" {
				env = append(env, prefix+suffix+"="+value)
				if suffix == "_KEY" {
					keys[consumer.Name] = value
				}
			}
		}
	}
	for _, step := range append(append([]testHTTPRequest{}, scenario.Requests...), scenario.Checks...) {
		if step.As != "" && keys[step.As] == "" {
			key := "GREGALE_TEST_CONSUMER_" + strings.ToUpper(strings.ReplaceAll(step.As, "-", "_")) + "_KEY"
			return nil, fmt.Errorf("local consumer %q needs %s in the environment", step.As, key)
		}
	}
	return env, nil
}

func runLocalTest(parent context.Context, name string, scenario testScenario, manifestDir, baseURL string, data testDataCase) (receipt testRunReceipt) {
	receipt = testRunReceipt{Scenario: name, Profile: "local", Engine: "local", Case: data.Name, Status: "failed", StartedAt: time.Now().UTC()}
	phases := newTestPhaseRecorder("setup")
	defer func() {
		receipt.Phases = phases.finish(receipt.Status)
		receipt.FinishedAt = time.Now().UTC()
		receipt.DurationMS = receipt.FinishedAt.Sub(receipt.StartedAt).Milliseconds()
	}()
	var err error
	baseURL, err = validateLocalTestURL(baseURL)
	if err != nil {
		receipt.Error = err.Error()
		return
	}
	if err := validateLocalTestScenario(scenario); err != nil {
		receipt.Error = err.Error()
		return
	}
	consumerEnv, err := localTestConsumerEnv(scenario)
	if err != nil {
		receipt.Error = err.Error()
		return
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		receipt.Error = fmt.Sprintf("create local run identity: %v", err)
		return
	}
	receipt.RunID = hex.EncodeToString(random)
	source := scenario.Source
	if source == "" {
		source = "."
	}
	sourceDir, err := resolveDeploySourceDir(manifestDir, source)
	if err != nil {
		receipt.Error = fmt.Sprintf("local source directory: %v", err)
		return
	}
	timeout := 15 * time.Minute
	if scenario.Timeout != "" {
		timeout, _ = time.ParseDuration(scenario.Timeout)
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	caseValues := data.Values
	if caseValues == nil {
		caseValues = map[string]any{}
	}
	dataJSON, err := json.Marshal(caseValues)
	if err != nil {
		receipt.Error = fmt.Sprintf("encode case data: %v", err)
		return
	}
	env := append(testCommandBaseEnv(), consumerEnv...)
	env = append(env,
		"GREGALE_TEST_URL="+baseURL,
		"GREGALE_TEST_RUN_ID="+receipt.RunID,
		"GREGALE_TEST_ENGINE=local",
		"GREGALE_TEST_PROFILE=local",
		"GREGALE_TEST_SCENARIO="+name,
		"GREGALE_TEST_CASE="+data.Name,
		"GREGALE_TEST_DATA_JSON="+string(dataJSON),
	)
	defer func() {
		phases.advance("cleanup", receipt.Status)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cleanupCancel()
		for _, command := range scenario.Cleanup {
			if err := runTestCommand(cleanupCtx, sourceDir, env, command); err != nil {
				receipt.addCleanupError(fmt.Sprintf("local fixture cleanup: %v", err))
			}
		}
	}()
	for _, command := range scenario.Setup {
		if err := runTestCommand(ctx, sourceDir, env, command); err != nil {
			receipt.Error = fmt.Sprintf("local fixture setup: %v", err)
			return
		}
	}
	phases.advance("requests", "passed")
	if len(scenario.Trigger) > 0 {
		if err := runTestCommand(ctx, sourceDir, env, scenario.Trigger); err != nil {
			receipt.Error = fmt.Sprintf("local trigger command: %v", err)
			return
		}
	}
	captures := make(map[string]string)
	requestEvidence, err := runTestHTTPRequestsWithData(ctx, baseURL, receipt.RunID, consumerEnv, scenario.Requests, captures, data.Values)
	receipt.Requests = append(receipt.Requests, requestEvidence...)
	if err != nil {
		receipt.Error = fmt.Sprintf("local request: %v", err)
		return
	}
	phases.advance("checks", "passed")
	requestEvidence, err = runTestHTTPRequestsWithData(ctx, baseURL, receipt.RunID, consumerEnv, scenario.Checks, captures, data.Values)
	receipt.Requests = append(receipt.Requests, requestEvidence...)
	if err != nil {
		receipt.Error = fmt.Sprintf("local check: %v", err)
		return
	}
	if len(scenario.Command) > 0 {
		if err := runTestCommand(ctx, sourceDir, env, scenario.Command); err != nil {
			receipt.Error = fmt.Sprintf("local assertion command: %v", err)
			return
		}
	}
	receipt.Status = "passed"
	return
}
