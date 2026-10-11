-- name: SeedQualificationMarker :exec
INSERT INTO public.gregale_durable_qualification_probe (id, marker)
VALUES ($1, $2) ON CONFLICT (id) DO NOTHING;

-- name: ReadQualificationMarker :one
SELECT marker, counter FROM public.gregale_durable_qualification_probe WHERE id = $1;

-- name: AdvanceQualificationMarker :one
UPDATE public.gregale_durable_qualification_probe SET counter = counter + 1
WHERE id = $1 RETURNING counter;
-- name: IsDisposableQualificationCatalog :one
SELECT EXISTS (
 SELECT 1 FROM pg_catalog.pg_db_role_setting
 WHERE setrole=0 AND setdatabase=(SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database())
 AND sqlc.arg(expected_setting)::text=ANY(setconfig)
)::boolean AS disposable;
