-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-1645: reviewed source builds have no OCI image digest. Only a fenced
-- managed HTTPS verification task may carry the empty digest; its context
-- revision pins the immutable archive/build/rootfs identity instead.
ALTER TABLE app_tasks DROP CONSTRAINT app_tasks_image_digest_chk;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_image_digest_chk CHECK (
    octet_length(image_digest) BETWEEN 1 AND 255
    OR (image_digest = '' AND coalesce(
        kind = 'manual' AND NOT command_shell
        AND binding_verification->>'type' = 'service'
        AND jsonb_typeof(binding_verification->'managed_service') = 'object'
        AND binding_verification->'managed_service'->>'version' = '1'
        AND binding_verification->'managed_service'->>'caller_deployment_id' = deployment_id::text
        AND binding_verification->'managed_service'->>'scope' = deployment_scope
        AND binding_verification->'managed_service'->>'service' = binding_verification->>'binding'
        AND binding_verification->'managed_service'->>'target_deployment_id' = binding_verification->>'target_deployment_id'
        AND binding_verification->'managed_service'->>'context_revision' = binding_verification->>'revision'
        AND binding_verification->'managed_service'->>'release_id' ~ '^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$'
        AND binding_verification->'managed_service'->>'target_app_id' ~ '^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$',
        false))
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM app_tasks WHERE image_digest = '') THEN
        RAISE EXCEPTION 'source binding probe history prevents downgrade';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE app_tasks DROP CONSTRAINT app_tasks_image_digest_chk;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_image_digest_chk CHECK (
    octet_length(image_digest) BETWEEN 1 AND 255
);
