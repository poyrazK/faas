from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProgressiveRollout")


@_attrs_define
class ProgressiveRollout:
    """Health-gated stages for a boolean true rule. Promotion is manual by default; auto_advance opts into server-managed
    promotion after a full healthy evidence window. The rule rollout must equal stages[current_stage]; stages must
    strictly increase and end at 10000 basis points.

    """

    stages: list[int]
    """Strictly increasing rollout percentages in basis points"""
    current_stage: int
    """Zero-based active stage index."""
    minimum_used_requests: int
    """Minimum application-reported used requests for the targeted rule before promotion."""
    maximum_http_5xx_rate_basis_points: int
    """Highest allowed 5xx rate for the targeted rule; 100 basis points is one percent."""
    maximum_p95_latency_ms: int
    """Highest allowed conservative p95 latency bucket bound for the targeted rule."""
    window_seconds: int
    """Evidence lookback window; must fit within debugger retention."""
    auto_advance: bool | Unset = False
    """When true, the platform automatically advances one stage after the full observation window passes all
    evidence gates. Omitted or false keeps promotion manual."""

    def to_dict(self) -> dict[str, Any]:
        stages = self.stages

        current_stage = self.current_stage

        minimum_used_requests = self.minimum_used_requests

        maximum_http_5xx_rate_basis_points = self.maximum_http_5xx_rate_basis_points

        maximum_p95_latency_ms = self.maximum_p95_latency_ms

        window_seconds = self.window_seconds

        auto_advance = self.auto_advance

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "stages": stages,
                "current_stage": current_stage,
                "minimum_used_requests": minimum_used_requests,
                "maximum_http_5xx_rate_basis_points": maximum_http_5xx_rate_basis_points,
                "maximum_p95_latency_ms": maximum_p95_latency_ms,
                "window_seconds": window_seconds,
            }
        )
        if auto_advance is not UNSET:
            field_dict["auto_advance"] = auto_advance

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        stages = cast(list[int], d.pop("stages"))

        current_stage = d.pop("current_stage")

        minimum_used_requests = d.pop("minimum_used_requests")

        maximum_http_5xx_rate_basis_points = d.pop("maximum_http_5xx_rate_basis_points")

        maximum_p95_latency_ms = d.pop("maximum_p95_latency_ms")

        window_seconds = d.pop("window_seconds")

        auto_advance = d.pop("auto_advance", UNSET)

        progressive_rollout = cls(
            stages=stages,
            current_stage=current_stage,
            minimum_used_requests=minimum_used_requests,
            maximum_http_5xx_rate_basis_points=maximum_http_5xx_rate_basis_points,
            maximum_p95_latency_ms=maximum_p95_latency_ms,
            window_seconds=window_seconds,
            auto_advance=auto_advance,
        )

        return progressive_rollout
