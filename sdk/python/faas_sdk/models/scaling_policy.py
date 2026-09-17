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
    """Per-instance signal the engine watches for the scale-up trigger. Closed metric set: rps |
    concurrent_requests | queue_depth | p99_latency_ms. queue_depth is a per-worker backlog budget and is valid for
    job/worker apps. Empty/null = engine falls back to the legacy autoscale_target_rps / autoscale_target_cpu_pct
    columns. Worker-class apps reject concurrent_requests with 422 scaling_target_incompatible_with_workload_class
    (PR-D carve-out)."""
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

        scale_out_cooldown_s = self.scale_out_cooldown_s

        scale_in_cooldown_s = self.scale_in_cooldown_s

        concurrency_overflow: str | Unset = UNSET
        if not isinstance(self.concurrency_overflow, Unset):
            concurrency_overflow = self.concurrency_overflow

        max_queue_wait_ms = self.max_queue_wait_ms

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if min_instances is not UNSET:
            field_dict["min_instances"] = min_instances
        if max_instances is not UNSET:
            field_dict["max_instances"] = max_instances
        if target is not UNSET:
            field_dict["target"] = target
        if scale_out_cooldown_s is not UNSET:
            field_dict["scale_out_cooldown_s"] = scale_out_cooldown_s
        if scale_in_cooldown_s is not UNSET:
            field_dict["scale_in_cooldown_s"] = scale_in_cooldown_s
        if concurrency_overflow is not UNSET:
            field_dict["concurrency_overflow"] = concurrency_overflow
        if max_queue_wait_ms is not UNSET:
            field_dict["max_queue_wait_ms"] = max_queue_wait_ms

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

        scale_out_cooldown_s = d.pop("scale_out_cooldown_s", UNSET)

        scale_in_cooldown_s = d.pop("scale_in_cooldown_s", UNSET)

        _concurrency_overflow = d.pop("concurrency_overflow", UNSET)
        concurrency_overflow: ScalingPolicyConcurrencyOverflow | Unset
        if isinstance(_concurrency_overflow, Unset):
            concurrency_overflow = UNSET
        else:
            concurrency_overflow = check_scaling_policy_concurrency_overflow(_concurrency_overflow)

        max_queue_wait_ms = d.pop("max_queue_wait_ms", UNSET)

        scaling_policy = cls(
            min_instances=min_instances,
            max_instances=max_instances,
            target=target,
            scale_out_cooldown_s=scale_out_cooldown_s,
            scale_in_cooldown_s=scale_in_cooldown_s,
            concurrency_overflow=concurrency_overflow,
            max_queue_wait_ms=max_queue_wait_ms,
        )

        return scaling_policy
