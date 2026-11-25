-- Required when a pipeline Task runtime publishes linked recovery tools.
ALTER TABLE public.gregale_pipeline_tasks ADD COLUMN IF NOT EXISTS replaces_task_id uuid;
