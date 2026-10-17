-- name: NativeIssueTicketGet :one
SELECT * FROM native_issue_ticket_receipts WHERE account_id=$1 AND request_id=$2;
-- name: NativeIssueTicketCount :one
SELECT count(*) FROM native_issue_ticket_receipts WHERE issue_id=$1;
-- name: NativeIssueTicketInsert :one
INSERT INTO native_issue_ticket_receipts(account_id,request_id,app_id,issue_id,request_hash)
VALUES($1,$2,$3,$4,$5) ON CONFLICT(account_id,request_id) DO NOTHING RETURNING *;
-- name: NativeIssueTicketClaim :one
UPDATE native_issue_ticket_receipts SET state='dispatching',claim_id=sqlc.arg(claim_id),recover_after=sqlc.arg(recover_after),updated_at=now()
WHERE account_id=sqlc.arg(account_id) AND request_id=sqlc.arg(request_id)
AND ((NOT sqlc.arg(recovery)::boolean AND state='ready') OR (sqlc.arg(recovery)::boolean AND state='dispatching' AND recover_after<=now())) RETURNING *;
-- name: NativeIssueTicketFinish :one
UPDATE native_issue_ticket_receipts SET state=sqlc.arg(state),ticket_url=sqlc.arg(ticket_url),provider_id=sqlc.arg(provider_id),updated_at=now()
WHERE account_id=sqlc.arg(account_id) AND request_id=sqlc.arg(request_id) AND state='dispatching' AND claim_id=sqlc.arg(claim_id) RETURNING *;
-- name: NativeIssueTicketLinked :one
UPDATE native_issue_ticket_receipts SET state='linked',link_id=sqlc.arg(link_id),updated_at=now()
WHERE account_id=sqlc.arg(account_id) AND request_id=sqlc.arg(request_id) AND state='created' RETURNING *;
