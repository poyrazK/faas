package main

import (
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// resolveCPUIdentity checks cfg.CPUTemplate against this host's CPU and
// stamps the pair vmmd registers in compute_nodes (ADR-20261024225612).
//
// A template the host cannot carry stops vmmd: guests booted with it could
// crash, and their snapshots would be advertised as portable to nodes they
// cannot run on. Without a template an unreadable host CPU is only logged;
// the node then registers no signature, and schedd treats its template-less
// snapshots as legacy rows.
func resolveCPUIdentity(cfg *Config, readHostCPU func() (fcvm.HostCPU, error), log *slog.Logger) error {
	if readHostCPU == nil {
		readHostCPU = fcvm.ReadHostCPU
	}
	cfg.ComputeNode.CPUTemplate = cfg.CPUTemplate
	cfg.ComputeNode.CPUModel = ""
	host, err := readHostCPU()
	if err != nil {
		if cfg.CPUTemplate != "" {
			return fmt.Errorf("vmmd: cpu_template %q needs the host cpu: %w", cfg.CPUTemplate, err)
		}
		log.Warn("vmmd: could not read the host cpu; registering no cpu signature", "err", err)
		return nil
	}
	if err := fcvm.ValidateCPUTemplate(cfg.CPUTemplate, host); err != nil {
		return fmt.Errorf("vmmd: %w", err)
	}
	cfg.ComputeNode.CPUModel = host.Signature()
	log.Info("vmmd: cpu identity resolved", "cpu_template", cfg.CPUTemplate,
		"cpu_class", string(host.Class), "cpu_model", cfg.ComputeNode.CPUModel)
	return nil
}
