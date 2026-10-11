-- name: EventSubscriptionSchemaVersionsSet :exec
UPDATE event_subscriptions SET schema_versions=sqlc.arg(schema_versions)::text[],updated_at=clock_timestamp() WHERE id=sqlc.arg(subscription_id)::uuid;
