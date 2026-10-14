from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="AutomationTestStepResult")


@_attrs_define
class AutomationTestStepResult:
    """One assertion result and observed state, excluding input, output and error data."""

    step: str
    passed: bool
    loop: str | Unset = UNSET
    item_index: int | Unset = UNSET
    state: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        step = self.step

        passed = self.passed

        loop = self.loop

        item_index = self.item_index

        state = self.state

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "step": step,
                "passed": passed,
            }
        )
        if loop is not UNSET:
            field_dict["loop"] = loop
        if item_index is not UNSET:
            field_dict["item_index"] = item_index
        if state is not UNSET:
            field_dict["state"] = state

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        step = d.pop("step")

        passed = d.pop("passed")

        loop = d.pop("loop", UNSET)

        item_index = d.pop("item_index", UNSET)

        state = d.pop("state", UNSET)

        automation_test_step_result = cls(
            step=step,
            passed=passed,
            loop=loop,
            item_index=item_index,
            state=state,
        )

        return automation_test_step_result
