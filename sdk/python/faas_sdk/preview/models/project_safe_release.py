from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_safe_release_status import ProjectSafeReleaseStatus, check_project_safe_release_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_release_addition import ProjectReleaseAddition
    from ..models.project_release_command import ProjectReleaseCommand
    from ..models.project_release_configuration import ProjectReleaseConfiguration
    from ..models.project_release_verification import ProjectReleaseVerification
    from ..models.project_release_worker import ProjectReleaseWorker


T = TypeVar("T", bound="ProjectSafeRelease")


@_attrs_define
class ProjectSafeRelease:
    """Server-owned safe project release checkpoint. Activation occurs only after all zero-traffic candidates complete
    readiness.

    """

    status: ProjectSafeReleaseStatus
    previous_release_id: str
    ttl_seconds: int
    deadline: datetime.datetime
    add_services: bool | Unset = UNSET
    """Explicit opt-in to reserve new HTTP source services."""
    additions: list[ProjectReleaseAddition] | Unset = UNSET
    release_id: str | Unset = UNSET
    message: str | Unset = UNSET
    verification: ProjectReleaseVerification | Unset = UNSET
    """Snapshotted critical probes and durable post-cutover verification evidence."""
    configuration: ProjectReleaseConfiguration | Unset = UNSET
    """Paired release configuration fingerprints and committed version identities; values remain private to server
    storage."""
    release_command: ProjectReleaseCommand | Unset = UNSET
    """Planned backward-compatible migration identity and durable task outcome. Task logs remain on the app-task
    resource."""
    worker: ProjectReleaseWorker | Unset = UNSET
    """One graph-owned production push worker, its immutable binding intent and runtime admission evidence."""
    restored_release_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        previous_release_id = self.previous_release_id

        ttl_seconds = self.ttl_seconds

        deadline = self.deadline.isoformat()

        add_services = self.add_services

        additions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.additions, Unset):
            additions = []
            for additions_item_data in self.additions:
                additions_item = additions_item_data.to_dict()
                additions.append(additions_item)

        release_id = self.release_id

        message = self.message

        verification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.verification, Unset):
            verification = self.verification.to_dict()

        configuration: dict[str, Any] | Unset = UNSET
        if not isinstance(self.configuration, Unset):
            configuration = self.configuration.to_dict()

        release_command: dict[str, Any] | Unset = UNSET
        if not isinstance(self.release_command, Unset):
            release_command = self.release_command.to_dict()

        worker: dict[str, Any] | Unset = UNSET
        if not isinstance(self.worker, Unset):
            worker = self.worker.to_dict()

        restored_release_id = self.restored_release_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "previous_release_id": previous_release_id,
                "ttl_seconds": ttl_seconds,
                "deadline": deadline,
            }
        )
        if add_services is not UNSET:
            field_dict["add_services"] = add_services
        if additions is not UNSET:
            field_dict["additions"] = additions
        if release_id is not UNSET:
            field_dict["release_id"] = release_id
        if message is not UNSET:
            field_dict["message"] = message
        if verification is not UNSET:
            field_dict["verification"] = verification
        if configuration is not UNSET:
            field_dict["configuration"] = configuration
        if release_command is not UNSET:
            field_dict["release_command"] = release_command
        if worker is not UNSET:
            field_dict["worker"] = worker
        if restored_release_id is not UNSET:
            field_dict["restored_release_id"] = restored_release_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_release_addition import ProjectReleaseAddition
        from ..models.project_release_command import ProjectReleaseCommand
        from ..models.project_release_configuration import ProjectReleaseConfiguration
        from ..models.project_release_verification import ProjectReleaseVerification
        from ..models.project_release_worker import ProjectReleaseWorker

        d = dict(src_dict)
        status = check_project_safe_release_status(d.pop("status"))

        previous_release_id = d.pop("previous_release_id")

        ttl_seconds = d.pop("ttl_seconds")

        deadline = datetime.datetime.fromisoformat(d.pop("deadline"))

        add_services = d.pop("add_services", UNSET)

        _additions = d.pop("additions", UNSET)
        additions: list[ProjectReleaseAddition] | Unset = UNSET
        if _additions is not UNSET:
            additions = []
            for additions_item_data in _additions:
                additions_item = ProjectReleaseAddition.from_dict(additions_item_data)

                additions.append(additions_item)

        release_id = d.pop("release_id", UNSET)

        message = d.pop("message", UNSET)

        _verification = d.pop("verification", UNSET)
        verification: ProjectReleaseVerification | Unset
        if isinstance(_verification, Unset):
            verification = UNSET
        else:
            verification = ProjectReleaseVerification.from_dict(_verification)

        _configuration = d.pop("configuration", UNSET)
        configuration: ProjectReleaseConfiguration | Unset
        if isinstance(_configuration, Unset):
            configuration = UNSET
        else:
            configuration = ProjectReleaseConfiguration.from_dict(_configuration)

        _release_command = d.pop("release_command", UNSET)
        release_command: ProjectReleaseCommand | Unset
        if isinstance(_release_command, Unset):
            release_command = UNSET
        else:
            release_command = ProjectReleaseCommand.from_dict(_release_command)

        _worker = d.pop("worker", UNSET)
        worker: ProjectReleaseWorker | Unset
        if isinstance(_worker, Unset):
            worker = UNSET
        else:
            worker = ProjectReleaseWorker.from_dict(_worker)

        restored_release_id = d.pop("restored_release_id", UNSET)

        project_safe_release = cls(
            status=status,
            previous_release_id=previous_release_id,
            ttl_seconds=ttl_seconds,
            deadline=deadline,
            add_services=add_services,
            additions=additions,
            release_id=release_id,
            message=message,
            verification=verification,
            configuration=configuration,
            release_command=release_command,
            worker=worker,
            restored_release_id=restored_release_id,
        )

        project_safe_release.additional_properties = d
        return project_safe_release

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
