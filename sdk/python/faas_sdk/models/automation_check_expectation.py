from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.automation_check_expectation_state import (
    AutomationCheckExpectationState,
    check_automation_check_expectation_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_check_attempt import AutomationCheckAttempt


T = TypeVar("T", bound="AutomationCheckExpectation")


@_attrs_define
class AutomationCheckExpectation:
    """Requires at least one assertion. Loop item assertions use step <loop>/action, loop name and canonical zero-based
    item index.

    """

    step: str
    loop: str | Unset = UNSET
    item_index: int | Unset = UNSET
    state: AutomationCheckExpectationState | Unset = UNSET
    reason: str | Unset = UNSET
    when_matched: bool | Unset = UNSET
    output: Any | Unset = UNSET
    """Exact JSON assertion; explicit null differs from omitted."""
    attempt_count: int | Unset = UNSET
    attempts: list[AutomationCheckAttempt] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        step = self.step

        loop = self.loop

        item_index = self.item_index

        state: str | Unset = UNSET
        if not isinstance(self.state, Unset):
            state = self.state

        reason = self.reason

        when_matched = self.when_matched

        output = self.output

        attempt_count = self.attempt_count

        attempts: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.attempts, Unset):
            attempts = []
            for attempts_item_data in self.attempts:
                attempts_item = attempts_item_data.to_dict()
                attempts.append(attempts_item)

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
        if reason is not UNSET:
            field_dict["reason"] = reason
        if when_matched is not UNSET:
            field_dict["when_matched"] = when_matched
        if output is not UNSET:
            field_dict["output"] = output
        if attempt_count is not UNSET:
            field_dict["attempt_count"] = attempt_count
        if attempts is not UNSET:
            field_dict["attempts"] = attempts

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_check_attempt import AutomationCheckAttempt

        d = dict(src_dict)
        step = d.pop("step")

        loop = d.pop("loop", UNSET)

        item_index = d.pop("item_index", UNSET)

        _state = d.pop("state", UNSET)
        state: AutomationCheckExpectationState | Unset
        if isinstance(_state, Unset):
            state = UNSET
        else:
            state = check_automation_check_expectation_state(_state)

        reason = d.pop("reason", UNSET)

        when_matched = d.pop("when_matched", UNSET)

        output = d.pop("output", UNSET)

        attempt_count = d.pop("attempt_count", UNSET)

        _attempts = d.pop("attempts", UNSET)
        attempts: list[AutomationCheckAttempt] | Unset = UNSET
        if _attempts is not UNSET:
            attempts = []
            for attempts_item_data in _attempts:
                attempts_item = AutomationCheckAttempt.from_dict(attempts_item_data)

                attempts.append(attempts_item)

        automation_check_expectation = cls(
            step=step,
            loop=loop,
            item_index=item_index,
            state=state,
            reason=reason,
            when_matched=when_matched,
            output=output,
            attempt_count=attempt_count,
            attempts=attempts,
        )

        return automation_check_expectation
