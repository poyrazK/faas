# migrations/ — goose, numbered, append-only (spec §5)

Never edit a merged migration. Schema authored in spec §5; sqlc generates typed
queries against it. Every state column carries a CHECK constraint.
