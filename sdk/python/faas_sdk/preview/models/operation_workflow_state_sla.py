from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_state_sla_status import (
    OperationWorkflowStateSLAStatus,
    check_operation_workflow_state_sla_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowStateSLA")


@_attrs_define
class OperationWorkflowStateSLA:
    """Observational SLA for the current nonterminal state visit under its pinned contract. Consecutive same-state metadata
    reports preserve entry time. Known history establishes the current visit from revision 1 or a contiguous prior
    different state. Gaps or inconsistent times/versions/budgets yield unknown status and omit inferred
    timestamps/durations. Breached at due_at or later. Terminal states have no current SLA.

    """

    evaluated_at: datetime.datetime
    budget_seconds: int
    status: OperationWorkflowStateSLAStatus
    history_complete: bool
    """Retained evidence establishes the current state visit entry. Earlier workflow history can still be
    incomplete."""
    warning_percent: int | Unset = UNSET
    """Pinned warning percentage when configured."""
    warning_at: datetime.datetime | Unset = UNSET
    """Warning time for a known state visit with a configured threshold."""
    entered_at: datetime.datetime | Unset = UNSET
    due_at: datetime.datetime | Unset = UNSET
    elapsed_seconds: int | Unset = UNSET
    remaining_seconds: int | Unset = UNSET
    """Ceiling of remaining elapsed time. Zero at or after the due time."""
    breached_seconds: int | Unset = UNSET
    """Whole seconds elapsed beyond the budget."""

    def to_dict(self) -> dict[str, Any]:
        evaluated_at = self.evaluated_at.isoformat()

        budget_seconds = self.budget_seconds

        status: str = self.status

        history_complete = self.history_complete

        warning_percent = self.warning_percent

        warning_at: str | Unset = UNSET
        if not isinstance(self.warning_at, Unset):
            warning_at = self.warning_at.isoformat()

        entered_at: str | Unset = UNSET
        if not isinstance(self.entered_at, Unset):
            entered_at = self.entered_at.isoformat()

        due_at: str | Unset = UNSET
        if not isinstance(self.due_at, Unset):
            due_at = self.due_at.isoformat()

        elapsed_seconds = self.elapsed_seconds

        remaining_seconds = self.remaining_seconds

        breached_seconds = self.breached_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "evaluated_at": evaluated_at,
                "budget_seconds": budget_seconds,
                "status": status,
                "history_complete": history_complete,
            }
        )
        if warning_percent is not UNSET:
            field_dict["warning_percent"] = warning_percent
        if warning_at is not UNSET:
            field_dict["warning_at"] = warning_at
        if entered_at is not UNSET:
            field_dict["entered_at"] = entered_at
        if due_at is not UNSET:
            field_dict["due_at"] = due_at
        if elapsed_seconds is not UNSET:
            field_dict["elapsed_seconds"] = elapsed_seconds
        if remaining_seconds is not UNSET:
            field_dict["remaining_seconds"] = remaining_seconds
        if breached_seconds is not UNSET:
            field_dict["breached_seconds"] = breached_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        budget_seconds = d.pop("budget_seconds")

        status = check_operation_workflow_state_sla_status(d.pop("status"))

        history_complete = d.pop("history_complete")

        warning_percent = d.pop("warning_percent", UNSET)

        _warning_at = d.pop("warning_at", UNSET)
        warning_at: datetime.datetime | Unset
        if isinstance(_warning_at, Unset):
            warning_at = UNSET
        else:
            warning_at = datetime.datetime.fromisoformat(_warning_at)

        _entered_at = d.pop("entered_at", UNSET)
        entered_at: datetime.datetime | Unset
        if isinstance(_entered_at, Unset):
            entered_at = UNSET
        else:
            entered_at = datetime.datetime.fromisoformat(_entered_at)

        _due_at = d.pop("due_at", UNSET)
        due_at: datetime.datetime | Unset
        if isinstance(_due_at, Unset):
            due_at = UNSET
        else:
            due_at = datetime.datetime.fromisoformat(_due_at)

        elapsed_seconds = d.pop("elapsed_seconds", UNSET)

        remaining_seconds = d.pop("remaining_seconds", UNSET)

        breached_seconds = d.pop("breached_seconds", UNSET)

        operation_workflow_state_sla = cls(
            evaluated_at=evaluated_at,
            budget_seconds=budget_seconds,
            status=status,
            history_complete=history_complete,
            warning_percent=warning_percent,
            warning_at=warning_at,
            entered_at=entered_at,
            due_at=due_at,
            elapsed_seconds=elapsed_seconds,
            remaining_seconds=remaining_seconds,
            breached_seconds=breached_seconds,
        )

        return operation_workflow_state_sla
