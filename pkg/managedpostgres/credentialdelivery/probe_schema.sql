-- Installed only in the owned, disposable qualification database by the
-- migration credential. Never install this in the control-plane catalog.
CREATE TABLE IF NOT EXISTS public.gregale_durable_qualification_probe (
  id uuid PRIMARY KEY,
  marker text NOT NULL,
  counter bigint NOT NULL DEFAULT 0
);
