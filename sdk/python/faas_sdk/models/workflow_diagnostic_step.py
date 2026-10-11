from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_diagnostic_step_kind import WorkflowDiagnosticStepKind, check_workflow_diagnostic_step_kind
from ..models.workflow_diagnostic_step_status import WorkflowDiagnosticStepStatus, check_workflow_diagnostic_step_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowDiagnosticStep")


@_attrs_define
class WorkflowDiagnosticStep:
    """Persisted step status and action kind without customer values."""

    step_name: str
    status: WorkflowDiagnosticStepStatus
    kind: WorkflowDiagnosticStepKind
    attempt: int
    retry_base: int
    next_retry_at: datetime.datetime | Unset = UNSET
    next_check_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        step_name = self.step_name

        status: str = self.status

        kind: str = self.kind

        attempt = self.attempt

        retry_base = self.retry_base

        next_retry_at: str | Unset = UNSET
        if not isinstance(self.next_retry_at, Unset):
            next_retry_at = self.next_retry_at.isoformat()

        next_check_at: str | Unset = UNSET
        if not isinstance(self.next_check_at, Unset):
            next_check_at = self.next_check_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "step_name": step_name,
                "status": status,
                "kind": kind,
                "attempt": attempt,
                "retry_base": retry_base,
            }
        )
        if next_retry_at is not UNSET:
            field_dict["next_retry_at"] = next_retry_at
        if next_check_at is not UNSET:
            field_dict["next_check_at"] = next_check_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        step_name = d.pop("step_name")

        status = check_workflow_diagnostic_step_status(d.pop("status"))

        kind = check_workflow_diagnostic_step_kind(d.pop("kind"))

        attempt = d.pop("attempt")

        retry_base = d.pop("retry_base")

        _next_retry_at = d.pop("next_retry_at", UNSET)
        next_retry_at: datetime.datetime | Unset
        if isinstance(_next_retry_at, Unset):
            next_retry_at = UNSET
        else:
            next_retry_at = datetime.datetime.fromisoformat(_next_retry_at)

        _next_check_at = d.pop("next_check_at", UNSET)
        next_check_at: datetime.datetime | Unset
        if isinstance(_next_check_at, Unset):
            next_check_at = UNSET
        else:
            next_check_at = datetime.datetime.fromisoformat(_next_check_at)

        workflow_diagnostic_step = cls(
            step_name=step_name,
            status=status,
            kind=kind,
            attempt=attempt,
            retry_base=retry_base,
            next_retry_at=next_retry_at,
            next_check_at=next_check_at,
        )

        workflow_diagnostic_step.additional_properties = d
        return workflow_diagnostic_step

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
