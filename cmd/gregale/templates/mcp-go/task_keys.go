package main

import "context"

// Authenticate retained payloads before starting writers. Streaming rows bounds
// memory and the caller's startup context bounds the whole scan. Sampling only
// one v1 envelope would miss old keys because v1 does not carry a key ID.
func (runtime *mcpTaskRuntime) checkTaskPayloadKeys(ctx context.Context) error {
	rows, err := runtime.pool.Query(ctx, `SELECT task_id::text,arguments_encrypted,result_encrypted,error_encrypted,input_state_encrypted
 FROM gregale_mcp_tasks WHERE namespace=$1 AND expires_at>clock_timestamp()`, runtime.namespace)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var arguments, result, taskError, inputs []byte
		if err := rows.Scan(&id, &arguments, &result, &taskError, &inputs); err != nil {
			return err
		}
		for _, field := range []struct {
			name    string
			payload []byte
		}{
			{"arguments", arguments}, {"result", result}, {"error", taskError}, {"input-state", inputs},
		} {
			if field.payload == nil {
				continue
			}
			if _, err := runtime.decryptPayload(field.payload, id, field.name); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}
