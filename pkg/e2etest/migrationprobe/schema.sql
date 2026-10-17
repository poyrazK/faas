-- Shape after expansion; installed only in the isolated customer database.
CREATE TABLE public.gregale_migration_qualification (
 run_id uuid PRIMARY KEY,
 legacy_value text NOT NULL,
 current_value text NOT NULL,
 writes bigint NOT NULL DEFAULT 0 CHECK (writes >= 0)
);

CREATE TABLE public.gregale_promotion_execution (
 run_id uuid PRIMARY KEY,
 mode text NOT NULL CHECK (mode IN ('success','failure','hold')),
 released boolean NOT NULL,
 executions integer NOT NULL DEFAULT 0 CHECK (executions >= 0),
 applied boolean NOT NULL DEFAULT false
);
