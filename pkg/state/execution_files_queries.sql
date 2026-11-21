-- name: ExecutionFileGet :one
SELECT * FROM execution_files WHERE account_id=sqlc.arg(account_id) AND id=sqlc.arg(id);
-- name: ExecutionFilesAccount :many
SELECT * FROM execution_files WHERE account_id=sqlc.arg(account_id);
-- name: ExecutionFileInsert :exec
INSERT INTO execution_files (id,account_id,principal_id,execution_id,name,kind,status,size_bytes,sha256,storage_key,sealed_key,token,lease_expires_at,expires_at,created_at,updated_at)
VALUES (sqlc.arg(id),sqlc.arg(account_id),sqlc.narg(principal_id),sqlc.narg(execution_id),sqlc.arg(name),sqlc.arg(kind),sqlc.arg(status),sqlc.arg(size_bytes),sqlc.arg(sha256),sqlc.arg(storage_key),sqlc.arg(sealed_key),sqlc.narg(token),sqlc.narg(lease_expires_at),sqlc.arg(expires_at),sqlc.arg(created_at),sqlc.arg(updated_at));
-- name: ExecutionFileUploadFinish :one
UPDATE execution_files SET status='ready', token=NULL,lease_expires_at=NULL,updated_at=sqlc.arg(at) WHERE account_id=sqlc.arg(account_id) AND id=sqlc.arg(id) AND status='uploading' AND token=sqlc.arg(token) AND lease_expires_at>sqlc.arg(at) AND expires_at>sqlc.arg(at) RETURNING *;
-- name: ExecutionFileUploadFail :exec
UPDATE execution_files SET status='failed',updated_at=sqlc.arg(at) WHERE account_id=sqlc.arg(account_id) AND id=sqlc.arg(id) AND status='uploading' AND token=sqlc.arg(token);
-- name: ExecutionFileRevoke :execrows
UPDATE execution_files SET expires_at=LEAST(expires_at,sqlc.arg(at)),updated_at=sqlc.arg(at) WHERE account_id=sqlc.arg(account_id) AND id=sqlc.arg(id) AND (sqlc.arg(account_wide)::boolean OR principal_id IS NOT DISTINCT FROM sqlc.narg(principal_id)::uuid);
-- name: ExecutionFileOutputReady :execrows
UPDATE execution_files SET status='ready',size_bytes=sqlc.arg(size_bytes),sha256=sqlc.arg(sha256),lease_expires_at=NULL,updated_at=sqlc.arg(at) WHERE account_id=sqlc.arg(account_id) AND execution_id=sqlc.arg(execution_id) AND id=sqlc.arg(id) AND name=sqlc.arg(name) AND status='pending';
-- name: ExecutionFileOutputList :many
SELECT * FROM execution_files WHERE account_id=sqlc.arg(account_id) AND execution_id=sqlc.arg(execution_id) ORDER BY name;
-- name: ExecutionFileLink :exec
INSERT INTO execution_file_links(execution_id,file_id) VALUES(sqlc.arg(execution_id),sqlc.arg(file_id)) ON CONFLICT DO NOTHING;
-- name: ExecutionFileReservationBytes :one
SELECT COALESCE(sum(e.max_artifact_bytes),0)::bigint FROM executions e WHERE e.account_id=sqlc.arg(account_id) AND (status IN ('queued','restoring','running') OR EXISTS(SELECT 1 FROM execution_files f WHERE f.execution_id=e.id AND f.status<>'ready'));
-- name: ExecutionFileGCCandidates :many
SELECT f.* FROM execution_files f WHERE
 (NOT EXISTS(SELECT 1 FROM accounts a WHERE a.id=f.account_id) OR f.expires_at<=sqlc.arg(at) OR f.status IN ('failed','deleting') OR f.status='uploading' AND f.lease_expires_at<=sqlc.arg(at))
 AND (f.lease_expires_at IS NULL OR f.lease_expires_at<=sqlc.arg(safe_at))
 AND f.updated_at<=sqlc.arg(safe_at)
 AND NOT EXISTS (SELECT 1 FROM executions e WHERE e.id=f.execution_id AND e.status IN ('queued','restoring','running'))
 AND NOT EXISTS (SELECT 1 FROM execution_file_links l JOIN executions e ON e.id=l.execution_id WHERE l.file_id=f.id AND e.status IN ('queued','restoring','running'))
 ORDER BY f.account_id,f.id LIMIT sqlc.arg(batch_size);
-- name: ExecutionFileGCClaim :one
UPDATE execution_files SET status='deleting',token=sqlc.arg(token),lease_expires_at=sqlc.arg(lease_until),updated_at=sqlc.arg(at) WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id) AND updated_at=sqlc.arg(previous_at) RETURNING *;
-- name: ExecutionFileUnlink :exec
DELETE FROM execution_file_links WHERE file_id=sqlc.arg(id);
-- name: ExecutionFileDelete :execrows
DELETE FROM execution_files WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id) AND status='deleting' AND token=sqlc.arg(token);
-- name: ExecutionFileGCEligible :one
SELECT EXISTS(SELECT 1 FROM execution_files f WHERE f.account_id=sqlc.arg(account_id) AND f.id=sqlc.arg(id)
 AND (NOT EXISTS(SELECT 1 FROM accounts a WHERE a.id=f.account_id) OR f.expires_at<=sqlc.arg(at) OR f.status IN ('failed','deleting') OR f.status='uploading' AND f.lease_expires_at<=sqlc.arg(at))
 AND (f.lease_expires_at IS NULL OR f.lease_expires_at<=sqlc.arg(safe_at)) AND f.updated_at<=sqlc.arg(safe_at)
 AND NOT EXISTS(SELECT 1 FROM executions e WHERE e.id=f.execution_id AND e.status IN ('queued','restoring','running'))
 AND NOT EXISTS(SELECT 1 FROM execution_file_links l JOIN executions e ON e.id=l.execution_id WHERE l.file_id=f.id AND e.status IN ('queued','restoring','running')));
-- name: ExecutionFileUsageBytes :one
-- Both sums share one statement snapshot: a concurrent terminalization swaps
-- its reservation for ready bytes atomically and cannot disappear between reads.
SELECT ((SELECT COALESCE(sum(f.size_bytes),0) FROM execution_files f WHERE f.account_id=sqlc.arg(account_id) AND (f.kind='upload' OR f.status='ready')) +
        (SELECT COALESCE(sum(e.max_artifact_bytes),0) FROM executions e WHERE e.account_id=sqlc.arg(account_id) AND (e.status IN ('queued','restoring','running') OR EXISTS(SELECT 1 FROM execution_files f WHERE f.execution_id=e.id AND f.status<>'ready'))))::bigint AS total;
