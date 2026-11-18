// Package taskacl defines the starter Task schema's native role permissions.
package taskacl

// ReadTables returns the tables that the runtime or observer must be able to
// read. A new slice is returned so callers cannot change the policy.
func ReadTables(observer bool) []string {
	tables := []string{"gregale_mcp_tasks", "gregale_mcp_task_owners", "gregale_mcp_task_workers", "gregale_mcp_task_worker_handlers", "gregale_mcp_task_schema", "gregale_mcp_task_admission_namespaces"}
	if !observer {
		tables = append(tables, "gregale_mcp_task_fairness", "gregale_mcp_task_crypto_keys", "gregale_mcp_task_admission")
	}
	return tables
}

// Writable identifies the tables on which the runtime needs ordinary DML.
func Writable(table string) bool {
	switch table {
	case "gregale_mcp_tasks", "gregale_mcp_task_owners", "gregale_mcp_task_fairness", "gregale_mcp_task_workers", "gregale_mcp_task_worker_handlers":
		return true
	default:
		return false
	}
}
