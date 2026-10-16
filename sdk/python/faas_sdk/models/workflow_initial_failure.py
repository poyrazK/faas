from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.workflow_initial_failure_failure_status import (
    WorkflowInitialFailureFailureStatus,
    check_workflow_initial_failure_failure_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowInitialFailure")


@_attrs_define
class WorkflowInitialFailure:
    """One captured generation-zero workflow failure."""

    id: UUID
    app_id: UUID
    run_id: UUID
    workflow: str
    failure_status: WorkflowInitialFailureFailureStatus
    observed_at: datetime.datetime
    completed_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        run_id = str(self.run_id)

        workflow = self.workflow

        failure_status: str = self.failure_status

        observed_at = self.observed_at.isoformat()

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "run_id": run_id,
                "workflow": workflow,
                "failure_status": failure_status,
                "observed_at": observed_at,
            }
        )
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        run_id = UUID(d.pop("run_id"))

        workflow = d.pop("workflow")

        failure_status = check_workflow_initial_failure_failure_status(d.pop("failure_status"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        workflow_initial_failure = cls(
            id=id,
            app_id=app_id,
            run_id=run_id,
            workflow=workflow,
            failure_status=failure_status,
            observed_at=observed_at,
            completed_at=completed_at,
        )

        return workflow_initial_failure
