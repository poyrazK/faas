from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.set_automation_publish_policy_request_mode import (
    SetAutomationPublishPolicyRequestMode,
    check_set_automation_publish_policy_request_mode,
)

T = TypeVar("T", bound="SetAutomationPublishPolicyRequest")


@_attrs_define
class SetAutomationPublishPolicyRequest:
    """Versioned app policy change; requires an admin credential."""

    mode: SetAutomationPublishPolicyRequestMode
    expected_version: int

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        expected_version = self.expected_version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "mode": mode,
                "expected_version": expected_version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_set_automation_publish_policy_request_mode(d.pop("mode"))

        expected_version = d.pop("expected_version")

        set_automation_publish_policy_request = cls(
            mode=mode,
            expected_version=expected_version,
        )

        return set_automation_publish_policy_request
