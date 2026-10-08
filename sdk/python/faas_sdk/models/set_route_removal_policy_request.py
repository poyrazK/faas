from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.set_route_removal_policy_request_mode import (
    SetRouteRemovalPolicyRequestMode,
    check_set_route_removal_policy_request_mode,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="SetRouteRemovalPolicyRequest")


@_attrs_define
class SetRouteRemovalPolicyRequest:
    expected_revision: int
    mode: SetRouteRemovalPolicyRequestMode
    grace_period: str | Unset = UNSET
    """Go duration between 1h and 2160h; default 720h."""
    max_approval_age: str | Unset = UNSET
    """Go duration between 1m and 72h; default 1h."""

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        mode: str = self.mode

        grace_period = self.grace_period

        max_approval_age = self.max_approval_age

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "mode": mode,
            }
        )
        if grace_period is not UNSET:
            field_dict["grace_period"] = grace_period
        if max_approval_age is not UNSET:
            field_dict["max_approval_age"] = max_approval_age

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        mode = check_set_route_removal_policy_request_mode(d.pop("mode"))

        grace_period = d.pop("grace_period", UNSET)

        max_approval_age = d.pop("max_approval_age", UNSET)

        set_route_removal_policy_request = cls(
            expected_revision=expected_revision,
            mode=mode,
            grace_period=grace_period,
            max_approval_age=max_approval_age,
        )

        return set_route_removal_policy_request
