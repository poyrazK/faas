-- name: Lock :exec
SELECT pg_advisory_xact_lock(hashtextextended($1::text,0));
-- name: Account :one
SELECT id,email::text,plan,status,email_verified_at,mfa_enrolled_at FROM accounts WHERE id=$1 FOR SHARE;
-- name: Org :one
SELECT o.id,o.plan FROM orgs o JOIN org_memberships m ON m.org_id=o.id WHERE o.slug=$1 AND m.account_id=$2 AND m.removed_at IS NULL AND m.role IN ('owner','admin') AND NOT o.deleted_pending AND o.status='active' FOR SHARE OF o,m;
-- name: Staff :one
SELECT * FROM support_staff WHERE account_id=$1 FOR SHARE;
-- name: StaffList :many
SELECT s.*,a.email::text,a.status,a.email_verified_at,a.mfa_enrolled_at FROM support_staff s JOIN accounts a ON a.id=s.account_id ORDER BY s.account_id LIMIT $1;
-- name: StaffSave :exec
INSERT INTO support_staff(account_id,revision,role,profile) VALUES($1,$2,$3,$4) ON CONFLICT(account_id) DO UPDATE SET revision=EXCLUDED.revision,role=EXCLUDED.role,profile=EXCLUDED.profile;
-- name: Resources :many
SELECT * FROM support_resources WHERE kind=$1 ORDER BY id LIMIT $2;
-- name: Resource :one
SELECT * FROM support_resources WHERE id=$1;
-- name: ResourceSave :exec
INSERT INTO support_resources(id,kind,revision,data,updated_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET revision=EXCLUDED.revision,data=EXCLUDED.data,updated_at=EXCLUDED.updated_at;
-- name: Receipt :one
SELECT * FROM support_receipts WHERE id=$1;
-- name: ReceiptSave :exec
INSERT INTO support_receipts(id,actor_id,ticket_id,operation,resource_id,request_hash,result,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8);
-- name: Ticket :one
SELECT * FROM support_tickets WHERE id=$1;
-- name: TicketLock :one
SELECT * FROM support_tickets WHERE id=$1 FOR UPDATE;
-- name: TicketInsert :exec
INSERT INTO support_tickets(id,scope_key,account_id,org_id,created_by,subject,category,status,revision,priority,team_id,assignee_id,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,'open',1,$8,$9,$10,$11,$12,$12);
-- name: TicketSave :exec
UPDATE support_tickets SET status=$2,revision=revision+1,priority=$3,team_id=$4,assignee_id=$5,state=$6,updated_at=$7,resolved_at=$8 WHERE id=$1;
-- name: TicketList :many
SELECT t.* FROM support_tickets t WHERE (sqlc.arg(staff)::boolean OR t.scope_key=sqlc.arg(scope_key)) AND (sqlc.arg(status)::text='' OR t.status=sqlc.arg(status)) AND (sqlc.narg(team_id)::uuid IS NULL OR t.team_id=sqlc.narg(team_id)) AND (sqlc.narg(assignee_id)::uuid IS NULL OR t.assignee_id=sqlc.narg(assignee_id)) AND (sqlc.arg(search)::text='' OR to_tsvector('simple',t.subject) @@ websearch_to_tsquery('simple',sqlc.arg(search)) OR EXISTS(SELECT 1 FROM support_messages m WHERE m.ticket_id=t.id AND (m.author<>'note' OR sqlc.arg(staff)::boolean) AND NOT m.redacted AND to_tsvector('simple',m.body) @@ websearch_to_tsquery('simple',sqlc.arg(search)))) AND (sqlc.narg(cursor)::uuid IS NULL OR (t.created_at,t.id)<(SELECT x.created_at,x.id FROM support_tickets x WHERE x.id=sqlc.narg(cursor) AND (sqlc.arg(staff)::boolean OR x.scope_key=sqlc.arg(scope_key)))) ORDER BY t.created_at DESC,t.id DESC LIMIT sqlc.arg(page_limit);
-- name: RecentCount :one
SELECT count(*) FROM support_events e JOIN support_tickets t ON t.id=e.ticket_id WHERE t.scope_key=$1 AND e.created_at>=$2 AND ($3::text='' OR e.kind=$3);
-- name: MessageCount :one
SELECT count(*) FROM support_messages WHERE ticket_id=$1;
-- name: MessageInsert :exec
INSERT INTO support_messages(id,ticket_id,actor_id,author,body,created_at) VALUES($1,$2,$3,$4,$5,$6);
-- name: Messages :many
SELECT * FROM support_messages WHERE ticket_id=$1 AND sequence>$2 AND (author<>'note' OR sqlc.arg(staff)::boolean) ORDER BY sequence LIMIT sqlc.arg(page_limit);
-- name: Message :one
SELECT * FROM support_messages WHERE id=$1 AND ticket_id=$2;
-- name: RedactionCopies :many
SELECT id,author FROM support_messages WHERE ticket_id=$1 AND body=$2 AND NOT redacted ORDER BY id;
-- name: RedactMessages :exec
UPDATE support_messages SET body='[redacted]',redacted=true WHERE ticket_id=$1 AND id=ANY(sqlc.arg(ids)::uuid[]);
-- name: EventInsert :exec
INSERT INTO support_events(id,ticket_id,actor_id,kind,data,created_at) VALUES($1,$2,$3,$4,$5,$6);
-- name: Events :many
SELECT * FROM support_events WHERE ticket_id=$1 AND sequence>$2 ORDER BY sequence LIMIT $3;
-- name: AgentLoad :one
SELECT count(*) FROM support_tickets WHERE assignee_id=$1 AND status<>'resolved';
-- name: EpisodeSave :exec
INSERT INTO support_episodes(id,ticket_id,started_at,data) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET data=EXCLUDED.data;
-- name: ReportCount :one
SELECT count(*) FROM support_episodes WHERE started_at>=$1 AND started_at<$2;
-- name: ReportEpisodes :many
SELECT data FROM support_episodes WHERE started_at>=$1 AND started_at<$2 ORDER BY started_at,id LIMIT $3;
-- name: PreviewInsert :exec
INSERT INTO support_previews(id,ticket_id,actor_id,kind,snapshot,expires_at) VALUES($1,$2,$3,$4,$5,$6);
-- name: Preview :one
SELECT * FROM support_previews WHERE id=$1 AND ticket_id=$2 AND actor_id=$3 AND kind=$4;
-- name: PreviewCount :one
SELECT count(*) FROM support_previews WHERE actor_id=$1 AND expires_at>$2;
-- name: PreviewsDelete :exec
DELETE FROM support_previews WHERE ticket_id=$1;
-- name: ExpirePreviews :exec
DELETE FROM support_previews WHERE id IN(SELECT p.id FROM support_previews p WHERE p.expires_at<=$1 ORDER BY p.expires_at LIMIT $2);
-- name: Engineering :one
SELECT * FROM support_engineering WHERE ticket_id=$1;
-- name: EngineeringSave :exec
INSERT INTO support_engineering(ticket_id,revision,data) VALUES($1,$2,$3) ON CONFLICT(ticket_id) DO UPDATE SET revision=EXCLUDED.revision,data=EXCLUDED.data;
-- name: Satisfaction :one
SELECT * FROM support_satisfaction WHERE episode_id=$1;
-- name: SatisfactionSave :exec
INSERT INTO support_satisfaction(episode_id,actor_id,rating,created_at) VALUES($1,$2,$3,$4);
-- name: OutboxQueue :exec
INSERT INTO support_outbox(id,ticket_id,state,available_at,created_at) VALUES($1,$2,'queued',$3,$3) ON CONFLICT(id) DO NOTHING;
-- name: OutboxClaim :one
WITH ready AS(SELECT o.id FROM support_outbox o WHERE (o.state='queued' AND o.available_at<=$1) OR (o.state='inflight' AND o.lease_until<=$1) ORDER BY o.available_at,o.id FOR UPDATE SKIP LOCKED LIMIT 1)
UPDATE support_outbox SET state='inflight',attempts=attempts+1,lease_id=$2,lease_until=$3 WHERE id IN(SELECT id FROM ready) RETURNING *;
-- name: OutboxFinish :exec
UPDATE support_outbox SET state=$3,available_at=$4,lease_id=NULL,lease_until=NULL WHERE id=$1 AND lease_id=$2 AND state='inflight';
-- name: AppScope :one
SELECT a.id FROM apps a LEFT JOIN orgs o ON o.id=a.org_id WHERE a.id=$1 AND (sqlc.narg(org_id)::uuid IS NOT NULL AND a.org_id=sqlc.narg(org_id) OR sqlc.narg(account_id)::uuid IS NOT NULL AND (a.org_id IS NULL AND a.account_id=sqlc.narg(account_id) OR o.personal_org AND o.personal_owner_account_id=sqlc.narg(account_id))) FOR SHARE OF a;
-- name: DeploymentScope :one
SELECT id FROM deployments WHERE id=$1 AND app_id=$2 FOR SHARE;

-- name: ConfigEvent :exec
INSERT INTO support_admin_events(id,actor_id,kind,resource_id,data,created_at) VALUES($1,$2,$3,$4,$5,$6);
-- name: ConfigEvents :many
SELECT * FROM support_admin_events WHERE sequence>$1 ORDER BY sequence LIMIT $2;
-- name: PersonalOrg :one
SELECT id FROM orgs WHERE id=$1 AND personal_org AND personal_owner_account_id=$2 AND NOT deleted_pending AND status='active' FOR SHARE;
-- name: OutboxEvent :one
SELECT kind FROM support_events WHERE id=$1;
