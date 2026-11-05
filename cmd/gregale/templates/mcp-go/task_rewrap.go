package main

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (runtime *mcpTaskRuntime) ambiguousRewrapKeyID() bool {
	for _, key := range runtime.taskKeys[1:] {
		if key.id == runtime.taskKeys[0].id {
			return true
		}
	}
	return false
}

func (runtime *mcpTaskRuntime) rewrapCandidates(ctx context.Context, tx pgx.Tx, limit int) (pgx.Rows, string, bool, error) {
	if !runtime.ambiguousRewrapKeyID() {
		checks := []string{}
		for _, column := range []string{"arguments_encrypted", "result_encrypted", "error_encrypted", "input_state_encrypted"} {
			checks = append(checks, taskPayloadNeedsRewrapSQL(column))
		}
		rows, err := tx.Query(ctx, `SELECT task_id::text,arguments_encrypted,result_encrypted,error_encrypted,input_state_encrypted
		FROM gregale_mcp_tasks WHERE namespace=$1 AND expires_at>clock_timestamp() AND (`+strings.Join(checks, " OR ")+`)
		ORDER BY created_at LIMIT $3 FOR UPDATE SKIP LOCKED`, runtime.namespace, []byte(runtime.taskKeys[0].id), limit)
		return rows, "", false, err
	}
	// Same-ID native and explicit envelopes need authentication, not header
	// sampling. A bounded pass prevents already-converted rows starving later data.
	through := runtime.rewrapThrough
	if through == "" {
		err := tx.QueryRow(ctx, `SELECT task_id::text FROM gregale_mcp_tasks WHERE namespace=$1 AND expires_at>clock_timestamp() ORDER BY task_id DESC LIMIT 1`, runtime.namespace).Scan(&through)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, "", true, err
		}
	}
	var afterID, throughID any
	if runtime.rewrapAfter != "" {
		afterID = runtime.rewrapAfter
	}
	if through != "" {
		throughID = through
	}
	rows, err := tx.Query(ctx, `SELECT task_id::text,arguments_encrypted,result_encrypted,error_encrypted,input_state_encrypted
	FROM gregale_mcp_tasks WHERE namespace=$1 AND expires_at>clock_timestamp()
	AND ($2::uuid IS NULL OR task_id>$2::uuid) AND task_id<=$3::uuid
	ORDER BY task_id LIMIT $4 FOR UPDATE SKIP LOCKED`, runtime.namespace, afterID, throughID, limit)
	return rows, through, true, err
}

func (runtime *mcpTaskRuntime) rewrapPayload(value []byte, taskID, field string, maxBytes int) ([]byte, bool, error) {
	if value == nil {
		return nil, false, nil
	}
	active := &mcpTaskRuntime{namespace: runtime.namespace, taskKeys: runtime.taskKeys[:1]}
	if len(value) > 0 && value[0] == 2 {
		if plain, err := active.decryptPayload(value, taskID, field); err == nil {
			if len(plain) > maxBytes {
				return nil, false, errors.New("MCP task payload exceeds its configured size limit")
			}
			return value, false, nil
		}
	}
	plain, err := runtime.decryptPayload(value, taskID, field)
	if err != nil {
		return nil, false, err
	}
	if len(plain) > maxBytes {
		return nil, false, errors.New("MCP task payload exceeds its configured size limit")
	}
	encrypted, err := runtime.encryptPayload(plain, taskID, field)
	return encrypted, true, err
}
