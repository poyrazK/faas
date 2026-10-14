-- filename: 20261014195747120_canary_preset_adaptive.sql
--
-- ADR-1321: evidence-paced default rollouts. deployments.canary_preset
-- admits "adaptive", the balanced ladder whose stages advance on clean
-- circuit-breaker evidence after one minute. The catalog in
-- pkg/api/canary.AllowedCanaryPresets is the source of truth; this check
-- mirrors it. Every existing row carries a name the widened check still
-- admits.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_canary_preset_chk;
ALTER TABLE deployments ADD CONSTRAINT deployments_canary_preset_chk
    CHECK (canary_preset IN ('none','slow','balanced','aggressive','1-10-50-100','adaptive','custom')) NOT VALID;
ALTER TABLE deployments VALIDATE CONSTRAINT deployments_canary_preset_chk;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- An adaptive rollout keeps its ladder under the balanced name: same
-- percentages and timers, without the early advance.
UPDATE deployments SET canary_preset = 'balanced' WHERE canary_preset = 'adaptive';
ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_canary_preset_chk;
ALTER TABLE deployments ADD CONSTRAINT deployments_canary_preset_chk
    CHECK (canary_preset IN ('none','slow','balanced','aggressive','1-10-50-100','custom')) NOT VALID;
ALTER TABLE deployments VALIDATE CONSTRAINT deployments_canary_preset_chk;
-- +goose StatementEnd
