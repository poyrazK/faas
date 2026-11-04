from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_qualification_response_status import (
    ProjectEnvironmentQualificationResponseStatus,
    check_project_environment_qualification_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_environment_qualification_check import ProjectEnvironmentQualificationCheck
    from ..models.project_environment_qualification_response_secret_revision_hashes import (
        ProjectEnvironmentQualificationResponseSecretRevisionHashes,
    )
    from ..models.project_environment_qualification_response_workload_config_hashes import (
        ProjectEnvironmentQualificationResponseWorkloadConfigHashes,
    )


T = TypeVar("T", bound="ProjectEnvironmentQualificationResponse")


@_attrs_define
class ProjectEnvironmentQualificationResponse:
    """Non-secret, 24-hour qualification receipt for an immutable release set and the exact source configuration and per-
    workload secret revision snapshots probed.

    """

    id: UUID
    environment: str
    release_set_id: UUID
    configuration_version: int
    """Version tested; -1 marks a legacy receipt that predates configuration binding and cannot qualify for
    promotion."""
    configuration_hash: str
    """SHA-256 of canonical non-secret environment configuration; empty only for a legacy receipt that cannot
    qualify for promotion."""
    secret_revision_hashes: ProjectEnvironmentQualificationResponseSecretRevisionHashes
    """Opaque per-workload fingerprints of secret keys, version metadata, and managed credential generations; no
    secret values or value hashes are included. Empty only for a legacy receipt that cannot qualify for promotion.
   """
    status: ProjectEnvironmentQualificationResponseStatus
    checks: list[ProjectEnvironmentQualificationCheck]
    created_at: datetime.datetime
    expires_at: datetime.datetime
    workload_config_hashes: ProjectEnvironmentQualificationResponseWorkloadConfigHashes | Unset = UNSET
    """Immutable per-workload fingerprints bound to this qualification receipt."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        environment = self.environment

        release_set_id = str(self.release_set_id)

        configuration_version = self.configuration_version

        configuration_hash = self.configuration_hash

        secret_revision_hashes = self.secret_revision_hashes.to_dict()

        status: str = self.status

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        workload_config_hashes: dict[str, Any] | Unset = UNSET
        if not isinstance(self.workload_config_hashes, Unset):
            workload_config_hashes = self.workload_config_hashes.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "environment": environment,
                "release_set_id": release_set_id,
                "configuration_version": configuration_version,
                "configuration_hash": configuration_hash,
                "secret_revision_hashes": secret_revision_hashes,
                "status": status,
                "checks": checks,
                "created_at": created_at,
                "expires_at": expires_at,
            }
        )
        if workload_config_hashes is not UNSET:
            field_dict["workload_config_hashes"] = workload_config_hashes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_qualification_check import ProjectEnvironmentQualificationCheck
        from ..models.project_environment_qualification_response_secret_revision_hashes import (
            ProjectEnvironmentQualificationResponseSecretRevisionHashes,
        )
        from ..models.project_environment_qualification_response_workload_config_hashes import (
            ProjectEnvironmentQualificationResponseWorkloadConfigHashes,
        )

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        environment = d.pop("environment")

        release_set_id = UUID(d.pop("release_set_id"))

        configuration_version = d.pop("configuration_version")

        configuration_hash = d.pop("configuration_hash")

        secret_revision_hashes = ProjectEnvironmentQualificationResponseSecretRevisionHashes.from_dict(
            d.pop("secret_revision_hashes")
        )

        status = check_project_environment_qualification_response_status(d.pop("status"))

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = ProjectEnvironmentQualificationCheck.from_dict(checks_item_data)

            checks.append(checks_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        _workload_config_hashes = d.pop("workload_config_hashes", UNSET)
        workload_config_hashes: ProjectEnvironmentQualificationResponseWorkloadConfigHashes | Unset
        if isinstance(_workload_config_hashes, Unset):
            workload_config_hashes = UNSET
        else:
            workload_config_hashes = ProjectEnvironmentQualificationResponseWorkloadConfigHashes.from_dict(
                _workload_config_hashes
            )

        project_environment_qualification_response = cls(
            id=id,
            environment=environment,
            release_set_id=release_set_id,
            configuration_version=configuration_version,
            configuration_hash=configuration_hash,
            secret_revision_hashes=secret_revision_hashes,
            status=status,
            checks=checks,
            created_at=created_at,
            expires_at=expires_at,
            workload_config_hashes=workload_config_hashes,
        )

        project_environment_qualification_response.additional_properties = d
        return project_environment_qualification_response

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
