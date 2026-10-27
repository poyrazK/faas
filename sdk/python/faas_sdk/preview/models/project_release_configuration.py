from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_release_secret_configuration import ProjectReleaseSecretConfiguration


T = TypeVar("T", bound="ProjectReleaseConfiguration")


@_attrs_define
class ProjectReleaseConfiguration:
    """Paired release configuration fingerprints and committed version identities; values remain private to server storage."""

    previous_version: int
    previous_hash: str
    target_hash: str
    environment_hash: str
    secrets: ProjectReleaseSecretConfiguration | Unset = UNSET
    """Candidate and baseline immutable secret version references bound to an atomic release."""
    request_hash: str | Unset = UNSET
    target_version: int | Unset = UNSET
    restored_version: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        previous_version = self.previous_version

        previous_hash = self.previous_hash

        target_hash = self.target_hash

        environment_hash = self.environment_hash

        secrets: dict[str, Any] | Unset = UNSET
        if not isinstance(self.secrets, Unset):
            secrets = self.secrets.to_dict()

        request_hash = self.request_hash

        target_version = self.target_version

        restored_version = self.restored_version

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "previous_version": previous_version,
                "previous_hash": previous_hash,
                "target_hash": target_hash,
                "environment_hash": environment_hash,
            }
        )
        if secrets is not UNSET:
            field_dict["secrets"] = secrets
        if request_hash is not UNSET:
            field_dict["request_hash"] = request_hash
        if target_version is not UNSET:
            field_dict["target_version"] = target_version
        if restored_version is not UNSET:
            field_dict["restored_version"] = restored_version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_release_secret_configuration import ProjectReleaseSecretConfiguration

        d = dict(src_dict)
        previous_version = d.pop("previous_version")

        previous_hash = d.pop("previous_hash")

        target_hash = d.pop("target_hash")

        environment_hash = d.pop("environment_hash")

        _secrets = d.pop("secrets", UNSET)
        secrets: ProjectReleaseSecretConfiguration | Unset
        if isinstance(_secrets, Unset):
            secrets = UNSET
        else:
            secrets = ProjectReleaseSecretConfiguration.from_dict(_secrets)

        request_hash = d.pop("request_hash", UNSET)

        target_version = d.pop("target_version", UNSET)

        restored_version = d.pop("restored_version", UNSET)

        project_release_configuration = cls(
            previous_version=previous_version,
            previous_hash=previous_hash,
            target_hash=target_hash,
            environment_hash=environment_hash,
            secrets=secrets,
            request_hash=request_hash,
            target_version=target_version,
            restored_version=restored_version,
        )

        project_release_configuration.additional_properties = d
        return project_release_configuration

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
