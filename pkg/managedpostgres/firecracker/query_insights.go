package firecracker

// QueryInsightsDatabase is the engine database native workload roles use.
func (p *Provider) QueryInsightsDatabase() string { return p.cfg.Database }
