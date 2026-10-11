from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.automation_simulation_step_kind import AutomationSimulationStepKind, check_automation_simulation_step_kind
from ..models.automation_simulation_step_state import (
    AutomationSimulationStepState,
    check_automation_simulation_step_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_simulation_attempt import AutomationSimulationAttempt


T = TypeVar("T", bound="AutomationSimulationStep")


@_attrs_define
class AutomationSimulationStep:
    """One hypothetical root or loop item with resolved data and its control-flow decision."""

    step_name: str
    kind: AutomationSimulationStepKind
    state: AutomationSimulationStepState
    reason: str | Unset = UNSET
    """Stable decision reason with no referenced customer values."""
    blocked_by: list[str] | Unset = UNSET
    when_matched: bool | Unset = UNSET
    input_: Any | Unset = UNSET
    """Resolved action input or materialized loop source; any JSON type is preserved."""
    output: Any | Unset = UNSET
    """Supplied successful mock, timeout sentinel or control output; omitted when no result is known."""
    attempts: list[AutomationSimulationAttempt] | Unset = UNSET
    """Safe summary of supplied action or wait outcomes; mocked error text is exposed only through eligible failure
    context."""
    run: str | Unset = UNSET
    path: str | Unset = UNSET
    method: str | Unset = UNSET
    integration_id: str | Unset = UNSET
    raw_query: str | Unset = UNSET
    """Canonically URL-encoded outbound query after resolving sample input and dependency outputs; omitted when
    empty or unresolved."""
    wait_for: str | Unset = UNSET
    parent_step: str | Unset = UNSET
    item_index: int | Unset = UNSET
    item_count: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        step_name = self.step_name

        kind: str = self.kind

        state: str = self.state

        reason = self.reason

        blocked_by: list[str] | Unset = UNSET
        if not isinstance(self.blocked_by, Unset):
            blocked_by = self.blocked_by

        when_matched = self.when_matched

        input_ = self.input_

        output = self.output

        attempts: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.attempts, Unset):
            attempts = []
            for attempts_item_data in self.attempts:
                attempts_item = attempts_item_data.to_dict()
                attempts.append(attempts_item)

        run = self.run

        path = self.path

        method = self.method

        integration_id = self.integration_id

        raw_query = self.raw_query

        wait_for = self.wait_for

        parent_step = self.parent_step

        item_index = self.item_index

        item_count = self.item_count

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "step_name": step_name,
                "kind": kind,
                "state": state,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if blocked_by is not UNSET:
            field_dict["blocked_by"] = blocked_by
        if when_matched is not UNSET:
            field_dict["when_matched"] = when_matched
        if input_ is not UNSET:
            field_dict["input"] = input_
        if output is not UNSET:
            field_dict["output"] = output
        if attempts is not UNSET:
            field_dict["attempts"] = attempts
        if run is not UNSET:
            field_dict["run"] = run
        if path is not UNSET:
            field_dict["path"] = path
        if method is not UNSET:
            field_dict["method"] = method
        if integration_id is not UNSET:
            field_dict["integration_id"] = integration_id
        if raw_query is not UNSET:
            field_dict["raw_query"] = raw_query
        if wait_for is not UNSET:
            field_dict["wait_for"] = wait_for
        if parent_step is not UNSET:
            field_dict["parent_step"] = parent_step
        if item_index is not UNSET:
            field_dict["item_index"] = item_index
        if item_count is not UNSET:
            field_dict["item_count"] = item_count

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_simulation_attempt import AutomationSimulationAttempt

        d = dict(src_dict)
        step_name = d.pop("step_name")

        kind = check_automation_simulation_step_kind(d.pop("kind"))

        state = check_automation_simulation_step_state(d.pop("state"))

        reason = d.pop("reason", UNSET)

        blocked_by = cast(list[str], d.pop("blocked_by", UNSET))

        when_matched = d.pop("when_matched", UNSET)

        input_ = d.pop("input", UNSET)

        output = d.pop("output", UNSET)

        _attempts = d.pop("attempts", UNSET)
        attempts: list[AutomationSimulationAttempt] | Unset = UNSET
        if _attempts is not UNSET:
            attempts = []
            for attempts_item_data in _attempts:
                attempts_item = AutomationSimulationAttempt.from_dict(attempts_item_data)

                attempts.append(attempts_item)

        run = d.pop("run", UNSET)

        path = d.pop("path", UNSET)

        method = d.pop("method", UNSET)

        integration_id = d.pop("integration_id", UNSET)

        raw_query = d.pop("raw_query", UNSET)

        wait_for = d.pop("wait_for", UNSET)

        parent_step = d.pop("parent_step", UNSET)

        item_index = d.pop("item_index", UNSET)

        item_count = d.pop("item_count", UNSET)

        automation_simulation_step = cls(
            step_name=step_name,
            kind=kind,
            state=state,
            reason=reason,
            blocked_by=blocked_by,
            when_matched=when_matched,
            input_=input_,
            output=output,
            attempts=attempts,
            run=run,
            path=path,
            method=method,
            integration_id=integration_id,
            raw_query=raw_query,
            wait_for=wait_for,
            parent_step=parent_step,
            item_index=item_index,
            item_count=item_count,
        )

        return automation_simulation_step
