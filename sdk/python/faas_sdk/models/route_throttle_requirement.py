from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.route_throttle_requirement_key_by import (
    RouteThrottleRequirementKeyBy,
    check_route_throttle_requirement_key_by,
)
from ..models.route_throttle_requirement_missing_key_policy import (
    RouteThrottleRequirementMissingKeyPolicy,
    check_route_throttle_requirement_missing_key_policy,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteThrottleRequirement")


@_attrs_define
class RouteThrottleRequirement:
    """Desired throttle key dimension, optional rate ceiling, and missing-identity behavior."""

    key_by: RouteThrottleRequirementKeyBy
    max_rps: float | Unset = UNSET
    missing_key_policy: RouteThrottleRequirementMissingKeyPolicy | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        key_by: str = self.key_by

        max_rps = self.max_rps

        missing_key_policy: str | Unset = UNSET
        if not isinstance(self.missing_key_policy, Unset):
            missing_key_policy = self.missing_key_policy

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key_by": key_by,
            }
        )
        if max_rps is not UNSET:
            field_dict["max_rps"] = max_rps
        if missing_key_policy is not UNSET:
            field_dict["missing_key_policy"] = missing_key_policy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key_by = check_route_throttle_requirement_key_by(d.pop("key_by"))

        max_rps = d.pop("max_rps", UNSET)

        _missing_key_policy = d.pop("missing_key_policy", UNSET)
        missing_key_policy: RouteThrottleRequirementMissingKeyPolicy | Unset
        if isinstance(_missing_key_policy, Unset):
            missing_key_policy = UNSET
        else:
            missing_key_policy = check_route_throttle_requirement_missing_key_policy(_missing_key_policy)

        route_throttle_requirement = cls(
            key_by=key_by,
            max_rps=max_rps,
            missing_key_policy=missing_key_policy,
        )

        return route_throttle_requirement
