from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.automation_check_exclusion_code import AutomationCheckExclusionCode, check_automation_check_exclusion_code
from ..types import UNSET, Unset

T = TypeVar("T", bound="AutomationCheckExclusion")


@_attrs_define
class AutomationCheckExclusion:
    """An explicitly reviewed coverage exclusion. Reasons are user-authored metadata and should contain no secrets."""

    step: str
    code: AutomationCheckExclusionCode
    reason: str
    loop: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        step = self.step

        code: str = self.code

        reason = self.reason

        loop = self.loop

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "step": step,
                "code": code,
                "reason": reason,
            }
        )
        if loop is not UNSET:
            field_dict["loop"] = loop

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        step = d.pop("step")

        code = check_automation_check_exclusion_code(d.pop("code"))

        reason = d.pop("reason")

        loop = d.pop("loop", UNSET)

        automation_check_exclusion = cls(
            step=step,
            code=code,
            reason=reason,
            loop=loop,
        )

        return automation_check_exclusion
