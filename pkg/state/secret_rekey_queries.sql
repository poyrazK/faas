-- name: ListSecretRekeyDocuments :many
SELECT kind::text AS kind,id::text AS id,payload::jsonb AS payload FROM release_secret_rekey_documents d
WHERE kind||'|'||id > sqlc.arg(after_cursor)::text
 AND EXISTS(SELECT 1 FROM release_secret_document_envelopes(d.payload,d.kind) e
 WHERE coalesce(e->>'kid','')<>sqlc.arg(current_kid)::text)
ORDER BY kind,id LIMIT sqlc.arg(batch_limit)::integer;

-- name: CompareAndSwapAppSecretEnvelope :execrows
UPDATE app_secrets s SET kid=sqlc.arg(replacement)::jsonb#>>'{0,kid}',
 ciphertext=decode(sqlc.arg(replacement)::jsonb#>>'{0,ciphertext}','base64')
WHERE s.account_id=split_part(sqlc.arg(document_id)::text,'/',1)::uuid
 AND s.app_id=split_part(sqlc.arg(document_id)::text,'/',2)::uuid
 AND s.scope=split_part(sqlc.arg(document_id)::text,'/',3)
 AND s.key=split_part(sqlc.arg(document_id)::text,'/',4)
 AND release_secret_app_document(s)=sqlc.arg(previous)::jsonb;

-- name: CompareAndSwapReleaseSecretBundle :execrows
UPDATE project_release_secret_bundles SET values_json=sqlc.arg(replacement)::jsonb
WHERE account_id=split_part(sqlc.arg(document_id)::text,'/',1)::uuid
 AND request_id=split_part(sqlc.arg(document_id)::text,'/',2)
 AND values_json=sqlc.arg(previous)::jsonb;

-- name: CompareAndSwapReleaseSecretCapture :execrows
UPDATE project_release_config_intents SET payload=sqlc.arg(replacement)::jsonb
WHERE account_id=split_part(sqlc.arg(document_id)::text,'/',1)::uuid
 AND attempt_id=split_part(sqlc.arg(document_id)::text,'/',2)
 AND payload=sqlc.arg(previous)::jsonb;

-- name: CompareAndSwapReleaseSecretSnapshot :execrows
UPDATE deployment_release_secret_snapshots SET values_json=sqlc.arg(replacement)::jsonb
WHERE deployment_id=sqlc.arg(document_id)::text::uuid AND values_json=sqlc.arg(previous)::jsonb;

-- name: LockSecretRekeyApp :exec
SELECT a.id FROM apps a WHERE
 (sqlc.arg(document_kind)::text='app' AND a.id::text=split_part(sqlc.arg(document_id)::text,'/',2))
 OR (sqlc.arg(document_kind)::text='deployment' AND EXISTS(SELECT 1 FROM deployments d WHERE d.id::text=sqlc.arg(document_id)::text AND d.app_id=a.id))
FOR UPDATE OF a;

-- name: EnableReleaseSecretRekey :exec
SELECT set_config('faas.release_secret_rekey','on',true);

-- name: LockSecretEncryptionKeyRetirement :exec
SELECT pg_advisory_xact_lock(hashtextextended('release-secret-key-retirement',0));

-- name: ReadSecretEncryptionKeyUsage :one
SELECT count(*) FILTER(WHERE kind='app')::bigint AS live_secrets,
 count(*) FILTER(WHERE kind='bundle')::bigint AS staged_bundles,
 count(*) FILTER(WHERE kind='deployment')::bigint AS deployment_snapshots,
 count(*) FILTER(WHERE kind IN ('capture','promotion'))::bigint AS release_captures
FROM release_secret_rekey_documents d
WHERE EXISTS(SELECT 1 FROM release_secret_document_envelopes(d.payload,d.kind) e
 WHERE coalesce(e->>'kid','') IN ('',sqlc.arg(kid)::text));

-- name: RetireSecretEncryptionKey :exec
INSERT INTO release_secret_key_retirements(kid) VALUES(sqlc.arg(kid)::text) ON CONFLICT(kid) DO NOTHING;

-- name: AdvanceSecretKeyRetirementFence :exec
UPDATE release_secret_key_retirement_fence SET generation=generation+1 WHERE singleton;

-- name: ReadSecretKeyRetirementFence :one
SELECT generation FROM release_secret_key_retirement_fence WHERE singleton;

-- name: CompareAndSwapPromotionSecretCapture :execrows
UPDATE project_environment_promotion_release_config SET payload=sqlc.arg(replacement)::jsonb
WHERE promotion_id=sqlc.arg(document_id)::text::uuid AND payload=sqlc.arg(previous)::jsonb;
