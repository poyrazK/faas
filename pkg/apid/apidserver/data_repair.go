package apidserver

import (
	"fmt"

	repairapi "github.com/onebox-faas/faas/pkg/apid/datarepair"
)

func (s *server) configureDataRepair(cfg Config, getenv func(string) string) error {
	path := cfg.DataRepairBindingsPath
	if v := getenv("FAAS_DATA_REPAIR_BINDINGS_PATH"); v != "" {
		path = v
	}
	bindings, err := repairapi.LoadBindings(path, getenv)
	if err != nil {
		return fmt.Errorf("configure managed data repair: invalid operator binding configuration")
	}
	s.dataRepair.Bindings = bindings
	return nil
}

// WithDataRepairBindings installs trusted executor bindings. Production loads
// these through data_repair_bindings_path; tests can supply private executors.
func (s *server) WithDataRepairBindings(bindings []repairapi.Binding) *server {
	s.dataRepair.Bindings = append([]repairapi.Binding(nil), bindings...)
	return s
}
