from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.flag_rollout_promotion_reason import FlagRolloutPromotionReason, check_flag_rollout_promotion_reason
from ..models.flag_rollout_promotion_status import FlagRolloutPromotionStatus, check_flag_rollout_promotion_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="FlagRolloutPromotion")


@_attrs_define
class FlagRolloutPromotion:
    """Health-gate evaluation for a progressive rollout stage. A held or complete status does not publish a new
    configuration version.

    """

    status: FlagRolloutPromotionStatus
    flag: str
    rule_id: str
    config_version: int
    """Current version after promotion, unchanged when held or complete."""
    current_stage: int
    """One-based ordinal of the currently configured stage."""
    stage_count: int
    rollout_basis_points: int
    """Active rollout percentage in basis points."""
    request_count: int
    used_count: int
    http_5xx_count: int
    http_5xx_rate: float
    """Observed 5xx count divided by the target rule request count."""
    p95_latency_ms: int
    latency_quantized: bool
    """True when p95 came from stored bucket bounds; false when no matching evidence was available."""
    minimum_used_requests: int
    maximum_http_5xx_rate_basis_points: int
    maximum_p95_latency_ms: int
    reason: FlagRolloutPromotionReason | Unset = UNSET
    next_rollout_basis_points: int | Unset = UNSET
    """Next configured percentage after promotion, omitted when already at the final stage."""
    window_start: datetime.datetime | Unset = UNSET
    window_end: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        flag = self.flag

        rule_id = self.rule_id

        config_version = self.config_version

        current_stage = self.current_stage

        stage_count = self.stage_count

        rollout_basis_points = self.rollout_basis_points

        request_count = self.request_count

        used_count = self.used_count

        http_5xx_count = self.http_5xx_count

        http_5xx_rate = self.http_5xx_rate

        p95_latency_ms = self.p95_latency_ms

        latency_quantized = self.latency_quantized

        minimum_used_requests = self.minimum_used_requests

        maximum_http_5xx_rate_basis_points = self.maximum_http_5xx_rate_basis_points

        maximum_p95_latency_ms = self.maximum_p95_latency_ms

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        next_rollout_basis_points = self.next_rollout_basis_points

        window_start: str | Unset = UNSET
        if not isinstance(self.window_start, Unset):
            window_start = self.window_start.isoformat()

        window_end: str | Unset = UNSET
        if not isinstance(self.window_end, Unset):
            window_end = self.window_end.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "flag": flag,
                "rule_id": rule_id,
                "config_version": config_version,
                "current_stage": current_stage,
                "stage_count": stage_count,
                "rollout_basis_points": rollout_basis_points,
                "request_count": request_count,
                "used_count": used_count,
                "http_5xx_count": http_5xx_count,
                "http_5xx_rate": http_5xx_rate,
                "p95_latency_ms": p95_latency_ms,
                "latency_quantized": latency_quantized,
                "minimum_used_requests": minimum_used_requests,
                "maximum_http_5xx_rate_basis_points": maximum_http_5xx_rate_basis_points,
                "maximum_p95_latency_ms": maximum_p95_latency_ms,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if next_rollout_basis_points is not UNSET:
            field_dict["next_rollout_basis_points"] = next_rollout_basis_points
        if window_start is not UNSET:
            field_dict["window_start"] = window_start
        if window_end is not UNSET:
            field_dict["window_end"] = window_end

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_flag_rollout_promotion_status(d.pop("status"))

        flag = d.pop("flag")

        rule_id = d.pop("rule_id")

        config_version = d.pop("config_version")

        current_stage = d.pop("current_stage")

        stage_count = d.pop("stage_count")

        rollout_basis_points = d.pop("rollout_basis_points")

        request_count = d.pop("request_count")

        used_count = d.pop("used_count")

        http_5xx_count = d.pop("http_5xx_count")

        http_5xx_rate = d.pop("http_5xx_rate")

        p95_latency_ms = d.pop("p95_latency_ms")

        latency_quantized = d.pop("latency_quantized")

        minimum_used_requests = d.pop("minimum_used_requests")

        maximum_http_5xx_rate_basis_points = d.pop("maximum_http_5xx_rate_basis_points")

        maximum_p95_latency_ms = d.pop("maximum_p95_latency_ms")

        _reason = d.pop("reason", UNSET)
        reason: FlagRolloutPromotionReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_flag_rollout_promotion_reason(_reason)

        next_rollout_basis_points = d.pop("next_rollout_basis_points", UNSET)

        _window_start = d.pop("window_start", UNSET)
        window_start: datetime.datetime | Unset
        if isinstance(_window_start, Unset):
            window_start = UNSET
        else:
            window_start = datetime.datetime.fromisoformat(_window_start)

        _window_end = d.pop("window_end", UNSET)
        window_end: datetime.datetime | Unset
        if isinstance(_window_end, Unset):
            window_end = UNSET
        else:
            window_end = datetime.datetime.fromisoformat(_window_end)

        flag_rollout_promotion = cls(
            status=status,
            flag=flag,
            rule_id=rule_id,
            config_version=config_version,
            current_stage=current_stage,
            stage_count=stage_count,
            rollout_basis_points=rollout_basis_points,
            request_count=request_count,
            used_count=used_count,
            http_5xx_count=http_5xx_count,
            http_5xx_rate=http_5xx_rate,
            p95_latency_ms=p95_latency_ms,
            latency_quantized=latency_quantized,
            minimum_used_requests=minimum_used_requests,
            maximum_http_5xx_rate_basis_points=maximum_http_5xx_rate_basis_points,
            maximum_p95_latency_ms=maximum_p95_latency_ms,
            reason=reason,
            next_rollout_basis_points=next_rollout_basis_points,
            window_start=window_start,
            window_end=window_end,
        )

        flag_rollout_promotion.additional_properties = d
        return flag_rollout_promotion

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
