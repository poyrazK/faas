from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.create_project_environment_qualification_request_secret_revision_hashes import (
        CreateProjectEnvironmentQualificationRequestSecretRevisionHashes,
    )
    from ..models.project_environment_qualification_check import ProjectEnvironmentQualificationCheck


T = TypeVar("T", bound="CreateProjectEnvironmentQualificationRequest")


@_attrs_define
class CreateProjectEnvironmentQualificationRequest:
    """Closed-schema health and smoke probe results for one exact active source release set, non-secret source
    configuration version, and per-workload secret revision fingerprints.

    """

    release_set_id: UUID
    configuration_version: int
    configuration_hash: str
    secret_revision_hashes: CreateProjectEnvironmentQualificationRequestSecretRevisionHashes
    checks: list[ProjectEnvironmentQualificationCheck]

    def to_dict(self) -> dict[str, Any]:
        release_set_id = str(self.release_set_id)

        configuration_version = self.configuration_version

        configuration_hash = self.configuration_hash

        secret_revision_hashes = self.secret_revision_hashes.to_dict()

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "release_set_id": release_set_id,
                "configuration_version": configuration_version,
                "configuration_hash": configuration_hash,
                "secret_revision_hashes": secret_revision_hashes,
                "checks": checks,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_project_environment_qualification_request_secret_revision_hashes import (
            CreateProjectEnvironmentQualificationRequestSecretRevisionHashes,
        )
        from ..models.project_environment_qualification_check import ProjectEnvironmentQualificationCheck

        d = dict(src_dict)
        release_set_id = UUID(d.pop("release_set_id"))

        configuration_version = d.pop("configuration_version")

        configuration_hash = d.pop("configuration_hash")

        secret_revision_hashes = CreateProjectEnvironmentQualificationRequestSecretRevisionHashes.from_dict(
            d.pop("secret_revision_hashes")
        )

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = ProjectEnvironmentQualificationCheck.from_dict(checks_item_data)

            checks.append(checks_item)

        create_project_environment_qualification_request = cls(
            release_set_id=release_set_id,
            configuration_version=configuration_version,
            configuration_hash=configuration_hash,
            secret_revision_hashes=secret_revision_hashes,
            checks=checks,
        )

        return create_project_environment_qualification_request
