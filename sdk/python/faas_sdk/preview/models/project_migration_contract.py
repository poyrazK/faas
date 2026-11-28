from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_migration_contract_status import (
    ProjectMigrationContractStatus,
    check_project_migration_contract_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_migration_recovery import ProjectMigrationRecovery


T = TypeVar("T", bound="ProjectMigrationContract")


@_attrs_define
class ProjectMigrationContract:
    """Guarded schema cleanup admission and durable task result. Cleanup command and task logs remain private to their
    existing resources.

    """

    status: ProjectMigrationContractStatus
    retired_deployments: int
    reason: str | Unset = UNSET
    request_id: str | Unset = UNSET
    expected_generation: int | Unset = UNSET
    task_id: str | Unset = UNSET
    exit_code: int | Unset = UNSET
    failure_code: str | Unset = UNSET
    recovery: ProjectMigrationRecovery | Unset = UNSET
    """Durable operator resolution and verified roll-forward identity. The cleanup outcome remains unchanged;
    database and execution checks are operator attestations."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        retired_deployments = self.retired_deployments

        reason = self.reason

        request_id = self.request_id

        expected_generation = self.expected_generation

        task_id = self.task_id

        exit_code = self.exit_code

        failure_code = self.failure_code

        recovery: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recovery, Unset):
            recovery = self.recovery.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "retired_deployments": retired_deployments,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if expected_generation is not UNSET:
            field_dict["expected_generation"] = expected_generation
        if task_id is not UNSET:
            field_dict["task_id"] = task_id
        if exit_code is not UNSET:
            field_dict["exit_code"] = exit_code
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if recovery is not UNSET:
            field_dict["recovery"] = recovery

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_migration_recovery import ProjectMigrationRecovery

        d = dict(src_dict)
        status = check_project_migration_contract_status(d.pop("status"))

        retired_deployments = d.pop("retired_deployments")

        reason = d.pop("reason", UNSET)

        request_id = d.pop("request_id", UNSET)

        expected_generation = d.pop("expected_generation", UNSET)

        task_id = d.pop("task_id", UNSET)

        exit_code = d.pop("exit_code", UNSET)

        failure_code = d.pop("failure_code", UNSET)

        _recovery = d.pop("recovery", UNSET)
        recovery: ProjectMigrationRecovery | Unset
        if isinstance(_recovery, Unset):
            recovery = UNSET
        else:
            recovery = ProjectMigrationRecovery.from_dict(_recovery)

        project_migration_contract = cls(
            status=status,
            retired_deployments=retired_deployments,
            reason=reason,
            request_id=request_id,
            expected_generation=expected_generation,
            task_id=task_id,
            exit_code=exit_code,
            failure_code=failure_code,
            recovery=recovery,
        )

        project_migration_contract.additional_properties = d
        return project_migration_contract

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
