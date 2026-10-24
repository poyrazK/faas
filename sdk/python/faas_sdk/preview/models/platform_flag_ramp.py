from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_flag_ramp_on_breach import PlatformFlagRampOnBreach, check_platform_flag_ramp_on_breach
from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformFlagRamp")


@_attrs_define
class PlatformFlagRamp:
    """Advances the rule's rollout stage by stage (ADR 20261021080640). The rollout is the current stage; apid moves it on
    once the flag's version has held for interval_seconds and its health routes stayed under max_5xx_basis_points over
    that window with at least min_requests requests. On a breach it pauses (and for rollback returns one stage); a
    paused ramp waits for an operator to clear paused.

    """

    stages: list[int]
    """Strictly ascending basis points; the rollout must be one of them."""
    interval_seconds: int
    max_5xx_basis_points: int
    min_requests: int
    on_breach: PlatformFlagRampOnBreach
    paused: bool | Unset = UNSET
    paused_reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        stages = self.stages

        interval_seconds = self.interval_seconds

        max_5xx_basis_points = self.max_5xx_basis_points

        min_requests = self.min_requests

        on_breach: str = self.on_breach

        paused = self.paused

        paused_reason = self.paused_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "stages": stages,
                "interval_seconds": interval_seconds,
                "max_5xx_basis_points": max_5xx_basis_points,
                "min_requests": min_requests,
                "on_breach": on_breach,
            }
        )
        if paused is not UNSET:
            field_dict["paused"] = paused
        if paused_reason is not UNSET:
            field_dict["paused_reason"] = paused_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        stages = cast(list[int], d.pop("stages"))

        interval_seconds = d.pop("interval_seconds")

        max_5xx_basis_points = d.pop("max_5xx_basis_points")

        min_requests = d.pop("min_requests")

        on_breach = check_platform_flag_ramp_on_breach(d.pop("on_breach"))

        paused = d.pop("paused", UNSET)

        paused_reason = d.pop("paused_reason", UNSET)

        platform_flag_ramp = cls(
            stages=stages,
            interval_seconds=interval_seconds,
            max_5xx_basis_points=max_5xx_basis_points,
            min_requests=min_requests,
            on_breach=on_breach,
            paused=paused,
            paused_reason=paused_reason,
        )

        platform_flag_ramp.additional_properties = d
        return platform_flag_ramp

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
