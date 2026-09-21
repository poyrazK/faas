from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.scaling_policy_concurrency_overflow import (
    ScalingPolicyConcurrencyOverflow,
    check_scaling_policy_concurrency_overflow,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.scaling_target import ScalingTarget


T = TypeVar("T", bound="ScalingPolicy")


@_attrs_define
class ScalingPolicy:
    """Per-app autoscaling configuration (issue #462 / ADR-058). Mirrors the on-disk jsonb column `apps.scaling_policy`.
    Empty values map to the engine default (the apid gate is load-bearing for the floor / ceiling, not the encoder).
    PR-A persists the DTO; PR-C wires the engine; PR-D carves out the worker-class branch.

    """

    min_instances: int | Unset = UNSET
    """Per-app cold-wake floor. 0 = scale to zero (default). Hobby+ unlocked at PR-A (was Pro/Scale pre-#462). Free
    → 403 plan_min_instances_not_allowed."""
    max_instances: int | Unset = UNSET
    """Per-app ceiling on live instances. Must be in [min_instances, plan.MaxConcurrency]. Hobby+ unlocked at PR-A.
    Free → 403 plan_max_instances_not_allowed. 0 = use plan max_concurrency."""
    target: None | ScalingTarget | Unset = UNSET
    """Single-signal form, superseded by `targets` (ADR-194) and still accepted: it is read as a one-element list.
    Closed metric set: rps | cpu | concurrent_requests | queue_depth. queue_depth is a per-worker backlog budget and
    is valid for job/worker apps. Empty/null = engine falls back to the legacy autoscale_target_rps /
    autoscale_target_cpu_pct columns. Worker-class apps reject concurrent_requests with 422
    scaling_target_incompatible_with_workload_class (PR-D carve-out). Mutually exclusive with `targets` — setting
    both is 422."""
    targets: list[ScalingTarget] | Unset = UNSET
    """Multi-signal autoscaling (ADR-194). Each entry states how much load ONE instance should carry on that
    metric; the platform evaluates every entry independently and provisions for the largest resulting instance
    count. The combination rule, the windowing and the cooldowns are platform policy and are not configurable per
    metric — declaring the signals is the whole surface. Each metric may appear at most once, every value must be >
    0, and cpu is a percentage capped at 100. Mutually exclusive with `target`."""
    scale_out_cooldown_s: int | Unset = UNSET
    """Minimum seconds between two scale-out events. Floor 1 (no 0 traps); ceiling 3600 (1 h). Out-of-range → 422
    invalid_cooldown."""
    scale_in_cooldown_s: int | Unset = UNSET
    """Minimum seconds between two scale-in events. Floor 5 (matches the reaper's 5 s idle window); ceiling 86400
    (1 day). Out-of-range → 422 invalid_cooldown."""
    concurrency_overflow: ScalingPolicyConcurrencyOverflow | Unset = UNSET
    """Behavior when the app concurrency boundary is saturated. queue waits up to max_queue_wait_ms; drop returns
    429 immediately. Empty uses queue."""
    max_queue_wait_ms: int | Unset = UNSET
    """Maximum admission wait in milliseconds. 0 uses the plan default; capped at 120000."""
    wake_max_queue_depth: int | Unset = UNSET
    """Per-app cold-wake waiter cap. 0 uses the plan default; positive values are capped at 8x the plan default."""
    wake_max_queue_wait_seconds: int | Unset = UNSET
    """Per-app cold-wake wait budget in seconds. 0 uses the plan default; capped at 60 seconds."""

    def to_dict(self) -> dict[str, Any]:
        from ..models.scaling_target import ScalingTarget

        min_instances = self.min_instances

        max_instances = self.max_instances

        target: dict[str, Any] | None | Unset
        if isinstance(self.target, Unset):
            target = UNSET
        elif isinstance(self.target, ScalingTarget):
            target = self.target.to_dict()
        else:
            target = self.target

        targets: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.targets, Unset):
            targets = []
            for targets_item_data in self.targets:
                targets_item = targets_item_data.to_dict()
                targets.append(targets_item)

        scale_out_cooldown_s = self.scale_out_cooldown_s

        scale_in_cooldown_s = self.scale_in_cooldown_s

        concurrency_overflow: str | Unset = UNSET
        if not isinstance(self.concurrency_overflow, Unset):
            concurrency_overflow = self.concurrency_overflow

        max_queue_wait_ms = self.max_queue_wait_ms

        wake_max_queue_depth = self.wake_max_queue_depth

        wake_max_queue_wait_seconds = self.wake_max_queue_wait_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if min_instances is not UNSET:
            field_dict["min_instances"] = min_instances
        if max_instances is not UNSET:
            field_dict["max_instances"] = max_instances
        if target is not UNSET:
            field_dict["target"] = target
        if targets is not UNSET:
            field_dict["targets"] = targets
        if scale_out_cooldown_s is not UNSET:
            field_dict["scale_out_cooldown_s"] = scale_out_cooldown_s
        if scale_in_cooldown_s is not UNSET:
            field_dict["scale_in_cooldown_s"] = scale_in_cooldown_s
        if concurrency_overflow is not UNSET:
            field_dict["concurrency_overflow"] = concurrency_overflow
        if max_queue_wait_ms is not UNSET:
            field_dict["max_queue_wait_ms"] = max_queue_wait_ms
        if wake_max_queue_depth is not UNSET:
            field_dict["wake_max_queue_depth"] = wake_max_queue_depth
        if wake_max_queue_wait_seconds is not UNSET:
            field_dict["wake_max_queue_wait_seconds"] = wake_max_queue_wait_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scaling_target import ScalingTarget

        d = dict(src_dict)
        min_instances = d.pop("min_instances", UNSET)

        max_instances = d.pop("max_instances", UNSET)

        def _parse_target(data: object) -> None | ScalingTarget | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                target_type_1 = ScalingTarget.from_dict(data)

                return target_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ScalingTarget | Unset, data)

        target = _parse_target(d.pop("target", UNSET))

        _targets = d.pop("targets", UNSET)
        targets: list[ScalingTarget] | Unset = UNSET
        if _targets is not UNSET:
            targets = []
            for targets_item_data in _targets:
                targets_item = ScalingTarget.from_dict(targets_item_data)

                targets.append(targets_item)

        scale_out_cooldown_s = d.pop("scale_out_cooldown_s", UNSET)

        scale_in_cooldown_s = d.pop("scale_in_cooldown_s", UNSET)

        _concurrency_overflow = d.pop("concurrency_overflow", UNSET)
        concurrency_overflow: ScalingPolicyConcurrencyOverflow | Unset
        if isinstance(_concurrency_overflow, Unset):
            concurrency_overflow = UNSET
        else:
            concurrency_overflow = check_scaling_policy_concurrency_overflow(_concurrency_overflow)

        max_queue_wait_ms = d.pop("max_queue_wait_ms", UNSET)

        wake_max_queue_depth = d.pop("wake_max_queue_depth", UNSET)

        wake_max_queue_wait_seconds = d.pop("wake_max_queue_wait_seconds", UNSET)

        scaling_policy = cls(
            min_instances=min_instances,
            max_instances=max_instances,
            target=target,
            targets=targets,
            scale_out_cooldown_s=scale_out_cooldown_s,
            scale_in_cooldown_s=scale_in_cooldown_s,
            concurrency_overflow=concurrency_overflow,
            max_queue_wait_ms=max_queue_wait_ms,
            wake_max_queue_depth=wake_max_queue_depth,
            wake_max_queue_wait_seconds=wake_max_queue_wait_seconds,
        )

        return scaling_policy
