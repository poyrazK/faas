-- Serialized with runtime authority under account -> execution -> attempt locks.
-- name: SaveCIJobsProgress :exec
UPDATE ci_job_attempts SET progress=$2,progress_sequence=$3,progress_digest=$4 WHERE attempt_id=$1;
