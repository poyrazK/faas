-- name: InitializeLegacy :exec
CREATE TABLE public.gregale_migration_qualification (
 run_id uuid PRIMARY KEY, legacy_value text NOT NULL,
 writes bigint NOT NULL DEFAULT 0 CHECK (writes >= 0)
);

-- name: SeedLegacy :exec
INSERT INTO public.gregale_migration_qualification(run_id,legacy_value) VALUES ($1,$2);

-- name: ExpandColumn :exec
ALTER TABLE public.gregale_migration_qualification ADD COLUMN current_value text;

-- name: BackfillCurrent :exec
UPDATE public.gregale_migration_qualification SET current_value=legacy_value;

-- name: RequireCurrent :exec
ALTER TABLE public.gregale_migration_qualification ALTER COLUMN current_value SET NOT NULL;

-- name: ContractLegacy :exec
ALTER TABLE public.gregale_migration_qualification DROP COLUMN legacy_value;

-- name: AdvanceLegacy :one
UPDATE public.gregale_migration_qualification SET writes=writes+1
WHERE run_id=$1 RETURNING legacy_value AS value,writes;

-- name: AdvanceCurrent :one
UPDATE public.gregale_migration_qualification SET writes=writes+1
WHERE run_id=$1 RETURNING current_value AS value,writes;

-- name: ReadLegacy :one
SELECT legacy_value AS value,writes FROM public.gregale_migration_qualification WHERE run_id=$1;

-- name: ReadCurrent :one
SELECT current_value AS value,writes FROM public.gregale_migration_qualification WHERE run_id=$1;

-- name: RuntimeDDL :exec
CREATE TABLE public.gregale_forbidden_runtime_ddl (id integer);

-- name: SchemaColumns :one
SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_attribute WHERE attrelid=to_regclass('public.gregale_migration_qualification')
AND attname='legacy_value' AND NOT attisdropped)::boolean AS legacy,
EXISTS(SELECT 1 FROM pg_catalog.pg_attribute WHERE attrelid=to_regclass('public.gregale_migration_qualification')
AND attname='current_value' AND NOT attisdropped)::boolean AS current;

-- name: ContractSessionName :exec
SELECT set_config('application_name', $1::text, false);

-- name: HoldContractTransaction :exec
SELECT pg_sleep(120);

-- name: ContractSessionHeld :one
SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE application_name=$1::text
AND wait_event='PgSleep' AND xact_start IS NOT NULL)::boolean AS held;

-- name: ReaderWrite :exec
UPDATE public.gregale_migration_qualification SET writes=writes WHERE run_id=$1;

-- name: InitializePromotion :exec
CREATE TABLE public.gregale_promotion_execution (
 run_id uuid PRIMARY KEY, mode text NOT NULL CHECK (mode IN ('success','failure','hold')),
 released boolean NOT NULL, executions integer NOT NULL DEFAULT 0 CHECK (executions >= 0),
 applied boolean NOT NULL DEFAULT false
);

-- name: SeedPromotion :exec
INSERT INTO public.gregale_promotion_execution(run_id,mode,released) VALUES ($1,$2,$3);

-- name: BeginPromotion :one
UPDATE public.gregale_promotion_execution SET executions=executions+1
WHERE run_id=$1 RETURNING mode,executions;

-- name: ApplyPromotion :exec
UPDATE public.gregale_promotion_execution SET applied=true WHERE run_id=$1;

-- name: ObservePromotion :one
SELECT mode,released,executions,applied FROM public.gregale_promotion_execution WHERE run_id=$1;

-- name: ReleasePromotion :exec
UPDATE public.gregale_promotion_execution SET released=true WHERE run_id=$1;

-- name: PromotionSessionAlive :one
SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE application_name=$1::text)::boolean AS alive;
