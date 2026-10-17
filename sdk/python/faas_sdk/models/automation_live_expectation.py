from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.automation_live_expectation_state import (
    AutomationLiveExpectationState,
    check_automation_live_expectation_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AutomationLiveExpectation")


@_attrs_define
class AutomationLiveExpectation:
    """A separate assertion against a real terminal workflow step, not a mock trace."""

    step: str
    loop: str | Unset = UNSET
    item_index: int | Unset = UNSET
    state: AutomationLiveExpectationState | Unset = UNSET
    output: Any | Unset = UNSET
    """Optional expected JSON result; an explicit null is asserted."""
    attempt_count: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        step = self.step

        loop = self.loop

        item_index = self.item_index

        state: str | Unset = UNSET
        if not isinstance(self.state, Unset):
            state = self.state

        output = self.output

        attempt_count = self.attempt_count

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "step": step,
            }
        )
        if loop is not UNSET:
            field_dict["loop"] = loop
        if item_index is not UNSET:
            field_dict["item_index"] = item_index
        if state is not UNSET:
            field_dict["state"] = state
        if output is not UNSET:
            field_dict["output"] = output
        if attempt_count is not UNSET:
            field_dict["attempt_count"] = attempt_count

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        step = d.pop("step")

        loop = d.pop("loop", UNSET)

        item_index = d.pop("item_index", UNSET)

        _state = d.pop("state", UNSET)
        state: AutomationLiveExpectationState | Unset
        if isinstance(_state, Unset):
            state = UNSET
        else:
            state = check_automation_live_expectation_state(_state)

        output = d.pop("output", UNSET)

        attempt_count = d.pop("attempt_count", UNSET)

        automation_live_expectation = cls(
            step=step,
            loop=loop,
            item_index=item_index,
            state=state,
            output=output,
            attempt_count=attempt_count,
        )

        return automation_live_expectation
