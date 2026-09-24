-- +goose Up
-- A push binding invokes an HTTP function through gatewayd. Preserve the
-- worker/job pull contract while permitting that function shape only for push.
ALTER TABLE queue_bindings DROP CONSTRAINT queue_bindings_workload_class_chk;
ALTER TABLE queue_bindings ADD CONSTRAINT queue_bindings_workload_class_chk
    CHECK (workload_class IN ('worker', 'job') OR
           (workload_class = 'http' AND mode = 'push'));

-- +goose Down
-- Forward-only: removing this class would strand existing function consumers.
SELECT 1;
