from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="AdvanceAutomationLiveSuiteRequest")


@_attrs_define
class AdvanceAutomationLiveSuiteRequest:
    """Explicit authorization to inspect existing identities and admit at most one remaining case. No target changes, run
    replacements or uploaded verdicts are accepted.

    """

    confirm_real_actions: bool

    def to_dict(self) -> dict[str, Any]:
        confirm_real_actions = self.confirm_real_actions

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "confirm_real_actions": confirm_real_actions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        confirm_real_actions = d.pop("confirm_real_actions")

        advance_automation_live_suite_request = cls(
            confirm_real_actions=confirm_real_actions,
        )

        return advance_automation_live_suite_request
