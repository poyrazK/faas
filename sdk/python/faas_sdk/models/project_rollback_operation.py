from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_rollback_operation_status import (
    ProjectRollbackOperationStatus,
    check_project_rollback_operation_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_rollback_auto_undo import ProjectRollbackAutoUndo
    from ..models.project_rollback_step import ProjectRollbackStep


T = TypeVar("T", bound="ProjectRollbackOperation")


@_attrs_define
class ProjectRollbackOperation:
    """A rollback plan or durable receipt with per-service cutovers and verification."""

    project: str
    project_id: UUID
    status: ProjectRollbackOperationStatus
    steps: list[ProjectRollbackStep]
    operation_id: UUID | Unset = UNSET
    request_id: UUID | Unset = UNSET
    """ The request ID saved when this rollback was accepted. """
    plan_hash: str | Unset = UNSET
    """ Hash identifying the runnable plan returned by preview or saved at admission. """
    resume_plan_hash: str | Unset = UNSET
    """ Hash from a runnable resume preview or the latest accepted reviewed resume. """
    undo_of: UUID | Unset = UNSET
    """ Source rollback whose cutovers this receipt undoes. """
    undo_operation_id: UUID | Unset = UNSET
    """ Accepted undo operation linked to this paused rollback. """
    deploy_group_id: UUID | Unset = UNSET
    acknowledge_releases: bool | Unset = UNSET
    verify: bool | Unset = UNSET
    """ Whether configured HTTP health verification is required. """
    verify_before_cutover: bool | Unset = UNSET
    """ Whether configured probes must pass before project traffic moves. """
    stabilize_for_seconds: int | Unset = UNSET
    """ Saved duration of the post-cutover health stabilization window. """
    verify_live_traffic: bool | Unset = UNSET
    """ Whether configured production 5xx budgets gate the rollback. """
    undo_on_health_failure: bool | Unset = UNSET
    """ Whether a confirmed health probe 5xx can trigger automatic undo. """
    undo_on_live_traffic_failure: bool | Unset = UNSET
    """ Whether a sustained production 5xx breach can trigger automatic undo. """
    auto_undo: ProjectRollbackAutoUndo | Unset = UNSET
    """ The trigger and outcome of an automatic undo after a confirmed health failure. """
    phase: str | Unset = UNSET
    """ Preparation phase of the operation, either preparing, cutover, or releasing. """
    pause_requested_at: datetime.datetime | Unset = UNSET
    """ When an operator requested the current pause. Cleared on resume. """
    preparation_deadline: datetime.datetime | Unset = UNSET
    """ Deadline for preparation """
    created_at: datetime.datetime | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    recovery_ms: int | Unset = UNSET
    """ Time from project rollback acceptance until the last targeted HTTP service was verified. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project = self.project

        project_id = str(self.project_id)

        status: str = self.status

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        operation_id: str | Unset = UNSET
        if not isinstance(self.operation_id, Unset):
            operation_id = str(self.operation_id)

        request_id: str | Unset = UNSET
        if not isinstance(self.request_id, Unset):
            request_id = str(self.request_id)

        plan_hash = self.plan_hash

        resume_plan_hash = self.resume_plan_hash

        undo_of: str | Unset = UNSET
        if not isinstance(self.undo_of, Unset):
            undo_of = str(self.undo_of)

        undo_operation_id: str | Unset = UNSET
        if not isinstance(self.undo_operation_id, Unset):
            undo_operation_id = str(self.undo_operation_id)

        deploy_group_id: str | Unset = UNSET
        if not isinstance(self.deploy_group_id, Unset):
            deploy_group_id = str(self.deploy_group_id)

        acknowledge_releases = self.acknowledge_releases

        verify = self.verify

        verify_before_cutover = self.verify_before_cutover

        stabilize_for_seconds = self.stabilize_for_seconds

        verify_live_traffic = self.verify_live_traffic

        undo_on_health_failure = self.undo_on_health_failure

        undo_on_live_traffic_failure = self.undo_on_live_traffic_failure

        auto_undo: dict[str, Any] | Unset = UNSET
        if not isinstance(self.auto_undo, Unset):
            auto_undo = self.auto_undo.to_dict()

        phase = self.phase

        pause_requested_at: str | Unset = UNSET
        if not isinstance(self.pause_requested_at, Unset):
            pause_requested_at = self.pause_requested_at.isoformat()

        preparation_deadline: str | Unset = UNSET
        if not isinstance(self.preparation_deadline, Unset):
            preparation_deadline = self.preparation_deadline.isoformat()

        created_at: str | Unset = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        recovery_ms = self.recovery_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project": project,
                "project_id": project_id,
                "status": status,
                "steps": steps,
            }
        )
        if operation_id is not UNSET:
            field_dict["operation_id"] = operation_id
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if plan_hash is not UNSET:
            field_dict["plan_hash"] = plan_hash
        if resume_plan_hash is not UNSET:
            field_dict["resume_plan_hash"] = resume_plan_hash
        if undo_of is not UNSET:
            field_dict["undo_of"] = undo_of
        if undo_operation_id is not UNSET:
            field_dict["undo_operation_id"] = undo_operation_id
        if deploy_group_id is not UNSET:
            field_dict["deploy_group_id"] = deploy_group_id
        if acknowledge_releases is not UNSET:
            field_dict["acknowledge_releases"] = acknowledge_releases
        if verify is not UNSET:
            field_dict["verify"] = verify
        if verify_before_cutover is not UNSET:
            field_dict["verify_before_cutover"] = verify_before_cutover
        if stabilize_for_seconds is not UNSET:
            field_dict["stabilize_for_seconds"] = stabilize_for_seconds
        if verify_live_traffic is not UNSET:
            field_dict["verify_live_traffic"] = verify_live_traffic
        if undo_on_health_failure is not UNSET:
            field_dict["undo_on_health_failure"] = undo_on_health_failure
        if undo_on_live_traffic_failure is not UNSET:
            field_dict["undo_on_live_traffic_failure"] = undo_on_live_traffic_failure
        if auto_undo is not UNSET:
            field_dict["auto_undo"] = auto_undo
        if phase is not UNSET:
            field_dict["phase"] = phase
        if pause_requested_at is not UNSET:
            field_dict["pause_requested_at"] = pause_requested_at
        if preparation_deadline is not UNSET:
            field_dict["preparation_deadline"] = preparation_deadline
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if recovery_ms is not UNSET:
            field_dict["recovery_ms"] = recovery_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_rollback_auto_undo import ProjectRollbackAutoUndo
        from ..models.project_rollback_step import ProjectRollbackStep

        d = dict(src_dict)
        project = d.pop("project")

        project_id = UUID(d.pop("project_id"))

        status = check_project_rollback_operation_status(d.pop("status"))

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = ProjectRollbackStep.from_dict(steps_item_data)

            steps.append(steps_item)

        _operation_id = d.pop("operation_id", UNSET)
        operation_id: UUID | Unset
        if isinstance(_operation_id, Unset):
            operation_id = UNSET
        else:
            operation_id = UUID(_operation_id)

        _request_id = d.pop("request_id", UNSET)
        request_id: UUID | Unset
        if isinstance(_request_id, Unset):
            request_id = UNSET
        else:
            request_id = UUID(_request_id)

        plan_hash = d.pop("plan_hash", UNSET)

        resume_plan_hash = d.pop("resume_plan_hash", UNSET)

        _undo_of = d.pop("undo_of", UNSET)
        undo_of: UUID | Unset
        if isinstance(_undo_of, Unset):
            undo_of = UNSET
        else:
            undo_of = UUID(_undo_of)

        _undo_operation_id = d.pop("undo_operation_id", UNSET)
        undo_operation_id: UUID | Unset
        if isinstance(_undo_operation_id, Unset):
            undo_operation_id = UNSET
        else:
            undo_operation_id = UUID(_undo_operation_id)

        _deploy_group_id = d.pop("deploy_group_id", UNSET)
        deploy_group_id: UUID | Unset
        if isinstance(_deploy_group_id, Unset):
            deploy_group_id = UNSET
        else:
            deploy_group_id = UUID(_deploy_group_id)

        acknowledge_releases = d.pop("acknowledge_releases", UNSET)

        verify = d.pop("verify", UNSET)

        verify_before_cutover = d.pop("verify_before_cutover", UNSET)

        stabilize_for_seconds = d.pop("stabilize_for_seconds", UNSET)

        verify_live_traffic = d.pop("verify_live_traffic", UNSET)

        undo_on_health_failure = d.pop("undo_on_health_failure", UNSET)

        undo_on_live_traffic_failure = d.pop("undo_on_live_traffic_failure", UNSET)

        _auto_undo = d.pop("auto_undo", UNSET)
        auto_undo: ProjectRollbackAutoUndo | Unset
        if isinstance(_auto_undo, Unset):
            auto_undo = UNSET
        else:
            auto_undo = ProjectRollbackAutoUndo.from_dict(_auto_undo)

        phase = d.pop("phase", UNSET)

        _pause_requested_at = d.pop("pause_requested_at", UNSET)
        pause_requested_at: datetime.datetime | Unset
        if isinstance(_pause_requested_at, Unset):
            pause_requested_at = UNSET
        else:
            pause_requested_at = datetime.datetime.fromisoformat(_pause_requested_at)

        _preparation_deadline = d.pop("preparation_deadline", UNSET)
        preparation_deadline: datetime.datetime | Unset
        if isinstance(_preparation_deadline, Unset):
            preparation_deadline = UNSET
        else:
            preparation_deadline = datetime.datetime.fromisoformat(_preparation_deadline)

        _created_at = d.pop("created_at", UNSET)
        created_at: datetime.datetime | Unset
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = datetime.datetime.fromisoformat(_created_at)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        recovery_ms = d.pop("recovery_ms", UNSET)

        project_rollback_operation = cls(
            project=project,
            project_id=project_id,
            status=status,
            steps=steps,
            operation_id=operation_id,
            request_id=request_id,
            plan_hash=plan_hash,
            resume_plan_hash=resume_plan_hash,
            undo_of=undo_of,
            undo_operation_id=undo_operation_id,
            deploy_group_id=deploy_group_id,
            acknowledge_releases=acknowledge_releases,
            verify=verify,
            verify_before_cutover=verify_before_cutover,
            stabilize_for_seconds=stabilize_for_seconds,
            verify_live_traffic=verify_live_traffic,
            undo_on_health_failure=undo_on_health_failure,
            undo_on_live_traffic_failure=undo_on_live_traffic_failure,
            auto_undo=auto_undo,
            phase=phase,
            pause_requested_at=pause_requested_at,
            preparation_deadline=preparation_deadline,
            created_at=created_at,
            updated_at=updated_at,
            recovery_ms=recovery_ms,
        )

        project_rollback_operation.additional_properties = d
        return project_rollback_operation

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
