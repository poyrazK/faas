-- name: GetCIExecutionProfile :one
SELECT * FROM ci_execution_profiles WHERE account_id=$1 AND id=$2;

-- name: GetCIExecutionProfileByName :one
SELECT * FROM ci_execution_profiles WHERE account_id=$1 AND name=$2;

-- name: ListCIExecutionProfiles :many
SELECT * FROM ci_execution_profiles WHERE account_id=$1
ORDER BY created_at DESC,id LIMIT sqlc.arg(max_rows);

-- name: CountCIExecutionProfiles :one
SELECT count(*) FROM ci_execution_profiles WHERE account_id=$1;

-- name: InsertCIExecutionProfile :one
INSERT INTO ci_execution_profiles(id,account_id,name,revision_id,revision_digest,binding_id,binding_generation,templates,snapshot)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING *;

-- name: DisableCIExecutionProfile :one
UPDATE ci_execution_profiles SET state='disabled' WHERE account_id=$1 AND id=$2 RETURNING *;
