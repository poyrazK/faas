from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_release_command_compatibility import (
    ProjectReleaseCommandCompatibility,
    check_project_release_command_compatibility,
)
from ..models.project_release_command_status import ProjectReleaseCommandStatus, check_project_release_command_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_migration_baseline_verification import ProjectMigrationBaselineVerification
    from ..models.project_migration_contract import ProjectMigrationContract


T = TypeVar("T", bound="ProjectReleaseCommand")


@_attrs_define
class ProjectReleaseCommand:
    """Planned backward-compatible migration identity and durable task outcome. Task logs remain on the app-task resource."""

    app_id: str
    slug: str
    command_hash: str
    compatibility: ProjectReleaseCommandCompatibility
    status: ProjectReleaseCommandStatus
    task_id: str | Unset = UNSET
    exit_code: int | Unset = UNSET
    failure_code: str | Unset = UNSET
    verify_baseline: bool | Unset = UNSET
    """Require attributed production baseline probes after migration and before publication."""
    baseline_verification: ProjectMigrationBaselineVerification | Unset = UNSET
    """Durable post-expansion production probe checkpoint. Commands and database contents remain private."""
    contract_command_hash: str | Unset = UNSET
    """Immutable hash of the source-declared cleanup command."""
    contract: ProjectMigrationContract | Unset = UNSET
    """Guarded schema cleanup admission and durable task result. Cleanup command and task logs remain private to
    their existing resources."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        slug = self.slug

        command_hash = self.command_hash

        compatibility: str = self.compatibility

        status: str = self.status

        task_id = self.task_id

        exit_code = self.exit_code

        failure_code = self.failure_code

        verify_baseline = self.verify_baseline

        baseline_verification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.baseline_verification, Unset):
            baseline_verification = self.baseline_verification.to_dict()

        contract_command_hash = self.contract_command_hash

        contract: dict[str, Any] | Unset = UNSET
        if not isinstance(self.contract, Unset):
            contract = self.contract.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "slug": slug,
                "command_hash": command_hash,
                "compatibility": compatibility,
                "status": status,
            }
        )
        if task_id is not UNSET:
            field_dict["task_id"] = task_id
        if exit_code is not UNSET:
            field_dict["exit_code"] = exit_code
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if verify_baseline is not UNSET:
            field_dict["verify_baseline"] = verify_baseline
        if baseline_verification is not UNSET:
            field_dict["baseline_verification"] = baseline_verification
        if contract_command_hash is not UNSET:
            field_dict["contract_command_hash"] = contract_command_hash
        if contract is not UNSET:
            field_dict["contract"] = contract

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_migration_baseline_verification import ProjectMigrationBaselineVerification
        from ..models.project_migration_contract import ProjectMigrationContract

        d = dict(src_dict)
        app_id = d.pop("app_id")

        slug = d.pop("slug")

        command_hash = d.pop("command_hash")

        compatibility = check_project_release_command_compatibility(d.pop("compatibility"))

        status = check_project_release_command_status(d.pop("status"))

        task_id = d.pop("task_id", UNSET)

        exit_code = d.pop("exit_code", UNSET)

        failure_code = d.pop("failure_code", UNSET)

        verify_baseline = d.pop("verify_baseline", UNSET)

        _baseline_verification = d.pop("baseline_verification", UNSET)
        baseline_verification: ProjectMigrationBaselineVerification | Unset
        if isinstance(_baseline_verification, Unset):
            baseline_verification = UNSET
        else:
            baseline_verification = ProjectMigrationBaselineVerification.from_dict(_baseline_verification)

        contract_command_hash = d.pop("contract_command_hash", UNSET)

        _contract = d.pop("contract", UNSET)
        contract: ProjectMigrationContract | Unset
        if isinstance(_contract, Unset):
            contract = UNSET
        else:
            contract = ProjectMigrationContract.from_dict(_contract)

        project_release_command = cls(
            app_id=app_id,
            slug=slug,
            command_hash=command_hash,
            compatibility=compatibility,
            status=status,
            task_id=task_id,
            exit_code=exit_code,
            failure_code=failure_code,
            verify_baseline=verify_baseline,
            baseline_verification=baseline_verification,
            contract_command_hash=contract_command_hash,
            contract=contract,
        )

        project_release_command.additional_properties = d
        return project_release_command

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
