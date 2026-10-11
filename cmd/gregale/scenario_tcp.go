package main

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func scenarioTCPPorts(scenario testScenario, workload string) []int {
	if workload == scenario.Project {
		return scenario.TCPPorts
	}
	return scenario.Services[workload].TCPPorts
}

type testTCPWakeClient interface {
	GetAppWakeTimeline(context.Context, string, api.AppWakeTimelineOptions) (api.AppWakeTimelineResponse, error)
}

// TCP streams do not create HTTP debug request rows. The warm lifecycle
// evidence establishes only that no dependency wake occurred; the application
// assertion must independently prove that its TCP exchange succeeded or failed.
func verifyTestServiceTCPHot(ctx context.Context, client testTCPWakeClient, slug string, baseline map[string]bool) (testServiceHotEvidence, error) {
	timeline, err := client.GetAppWakeTimeline(ctx, slug, api.AppWakeTimelineOptions{})
	if err != nil {
		return testServiceHotEvidence{}, err
	}
	if wakeID := firstNewTestWakeID(timeline.Rows, baseline); wakeID != "" {
		return testServiceHotEvidence{}, fmt.Errorf("TCP service woke during warm scenario (wake %s)", wakeID)
	}
	return testServiceHotEvidence{Transport: "tcp", NoNewWake: true}, nil
}

func validateScenarioTCPService(spec testService) error {
	ports := make([]api.WorkloadPort, 0, len(spec.TCPPorts))
	for _, port := range spec.TCPPorts {
		if slices.Contains(api.ServiceTCPReservedPorts(), port) {
			return fmt.Errorf("tcp_ports cannot include HTTP mesh reserved port %d", port)
		}
		ports = append(ports, api.WorkloadPort{Port: port, Protocol: api.WorkloadPortTCP, Internal: true})
	}
	if err := api.ValidateWorkloadPorts(ports); err != nil {
		return err
	}
	tcpFixture := spec.Fixture == testTCPRelayFixture || spec.Fixture == testTCPEchoFixture
	if tcpFixture && (len(spec.TCPPorts) != 1 || spec.TCPPorts[0] == api.DefaultAppPort) {
		return fmt.Errorf("TCP fixtures require one tcp_ports entry distinct from health port %d", api.DefaultAppPort)
	}
	if !tcpFixture && spec.Upstream != "" {
		return fmt.Errorf("upstream requires fixture tcp-relay")
	}
	if spec.Fixture == testTCPEchoFixture && spec.Upstream != "" {
		return fmt.Errorf("tcp-echo cannot declare an upstream")
	}
	if spec.Fixture == testTCPRelayFixture {
		host, port, err := net.SplitHostPort(spec.Upstream)
		number, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || number < 1 || number > 65535 || strings.ContainsAny(host, "/@\r\n$") || host == "" {
			return fmt.Errorf("tcp-relay upstream must be a fixed host:port without credentials or references")
		}
		if reason, forbidden := api.TenantEgressForbiddenPort(number); forbidden {
			return fmt.Errorf("tcp-relay upstream port %d is forbidden: %s", number, reason)
		}
	}
	for key := range spec.Secrets {
		if strings.HasPrefix(key, "GREGALE_TEST_TCP_") {
			return fmt.Errorf("reserved TCP fixture secret %q", key)
		}
	}
	if spec.Fixture != testTCPRelayFixture && len(spec.EgressAllowlist) > 0 {
		return fmt.Errorf("egress_allowlist is supported only on tcp-relay fixtures")
	}
	for _, entry := range spec.EgressAllowlist {
		if _, _, err := net.ParseCIDR(entry); err != nil {
			return fmt.Errorf("invalid relay egress CIDR %q", entry)
		}
	}
	return nil
}

// configureScenarioTCP declares private listeners before deployment. The relay
// gets exactly its upstream egress port; apid retains plan and denylist checks.
func configureScenarioTCP(ctx context.Context, client testAccessClient, slug string, spec testService) error {
	if len(spec.TCPPorts) == 0 {
		return nil
	}
	ports := make([]api.WorkloadPort, 0, len(spec.TCPPorts))
	for _, port := range spec.TCPPorts {
		ports = append(ports, api.WorkloadPort{Name: fmt.Sprintf("tcp-%d", port), Port: port, Protocol: api.WorkloadPortTCP, Internal: true})
	}
	req := api.UpdateAppRequest{Ports: &ports}
	if spec.Fixture == testTCPRelayFixture {
		host, port, _ := net.SplitHostPort(spec.Upstream)
		if !strings.HasSuffix(host, ".svc.gregale") {
			number, _ := strconv.Atoi(port)
			extra := []int{number}
			req.EgressPorts = &extra
		}
		if len(spec.EgressAllowlist) > 0 {
			req.EgressAllowlist = &spec.EgressAllowlist
		}
	}
	_, err := client.UpdateApp(ctx, slug, req)
	return err
}
