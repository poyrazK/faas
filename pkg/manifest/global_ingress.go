package manifest

// adr: 784
import "github.com/onebox-faas/faas/pkg/globalingress"

func (dc *DaemonConfig) globalIngressConfig() globalingress.Config {
	return globalingress.Config{DirectoryID: dc.GlobalIngressDirectoryID, AppsDomain: dc.GlobalIngressAppsDomain, ManifestPath: dc.GlobalIngressManifestPath, TrustBundlePath: dc.GlobalIngressTrustBundlePath}
}

func (m *Manifest) validateGlobalIngress() Errors {
	dc := m.Daemons.GatewaydInternal
	if dc == nil {
		return nil
	}
	c := dc.globalIngressConfig()
	if !c.Enabled() {
		return nil
	}
	var errs Errors
	if m.ControlPlaneCell != nil {
		errs = append(errs, Error{"daemons.gatewayd_internal.global_ingress_directory_id", "requires a separate unbound database; cannot be enabled in a regional cell"})
	}
	if err := c.Validate(m.DNS.AppsDomain); err != nil {
		errs = append(errs, Error{"daemons.gatewayd_internal.global_ingress_directory_id", err.Error()})
	}
	return errs
}
