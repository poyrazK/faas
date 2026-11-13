package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// parseAppPortsFlag reads `gregale app <slug> --ports`: a comma-separated
// list of [NAME=]tcp|udp:PORT[:internal], or "none" to clear. A listener
// without a name is named <protocol>-<port>, as Compose ports are. Before
// this flag a non-Compose app could only declare the TCP/UDP listener that
// `app listener create` and `app udp add` require through a raw API call
// (Hunt 34 H34-20).
func parseAppPortsFlag(value string) ([]api.WorkloadPort, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("--ports needs [NAME=]tcp|udp:PORT[:internal] entries, or none")
	}
	if strings.EqualFold(value, "none") {
		return []api.WorkloadPort{}, nil
	}
	var ports []api.WorkloadPort
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		name := ""
		if before, after, ok := strings.Cut(entry, "="); ok {
			name, entry = strings.ToLower(strings.TrimSpace(before)), strings.TrimSpace(after)
		}
		fields := strings.Split(entry, ":")
		if len(fields) < 2 || len(fields) > 3 || len(fields) == 3 && fields[2] != "internal" {
			return nil, fmt.Errorf("--ports entry %q: want [NAME=]tcp|udp:PORT[:internal]", entry)
		}
		protocol := api.WorkloadPortProtocol(strings.ToLower(fields[0]))
		if protocol != api.WorkloadPortTCP && protocol != api.WorkloadPortUDP {
			return nil, fmt.Errorf("--ports entry %q: protocol must be tcp or udp", entry)
		}
		port, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("--ports entry %q: port must be a number", entry)
		}
		if name == "" {
			name = fmt.Sprintf("%s-%d", protocol, port)
		}
		ports = append(ports, api.WorkloadPort{Name: name, Port: port, Protocol: protocol, Internal: len(fields) == 3})
	}
	if err := api.ValidateWorkloadPorts(ports); err != nil {
		return nil, fmt.Errorf("--ports: %w", err)
	}
	return ports, nil
}
