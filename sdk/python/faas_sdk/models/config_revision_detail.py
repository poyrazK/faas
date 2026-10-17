from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.config_revision_source import ConfigRevisionSource, check_config_revision_source
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.config_revision_detail_secret_versions import ConfigRevisionDetailSecretVersions
    from ..models.config_revision_detail_variables import ConfigRevisionDetailVariables


T = TypeVar("T", bound="ConfigRevisionDetail")


@_attrs_define
class ConfigRevisionDetail:
    """A config revision with its variables and secret versions. Secret values are never returned."""

    revision: int
    scope: str
    source: ConfigRevisionSource
    actor: str
    created_at: datetime.datetime
    variable_count: int
    secret_count: int
    variables: ConfigRevisionDetailVariables
    secret_versions: ConfigRevisionDetailSecretVersions
    key: str | Unset = UNSET
    guard_id: UUID | Unset = UNSET
    guard_status: str | Unset = UNSET
    restored_revision: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        scope = self.scope

        source: str = self.source

        actor = self.actor

        created_at = self.created_at.isoformat()

        variable_count = self.variable_count

        secret_count = self.secret_count

        variables = self.variables.to_dict()

        secret_versions = self.secret_versions.to_dict()

        key = self.key

        guard_id: str | Unset = UNSET
        if not isinstance(self.guard_id, Unset):
            guard_id = str(self.guard_id)

        guard_status = self.guard_status

        restored_revision = self.restored_revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "revision": revision,
                "scope": scope,
                "source": source,
                "actor": actor,
                "created_at": created_at,
                "variable_count": variable_count,
                "secret_count": secret_count,
                "variables": variables,
                "secret_versions": secret_versions,
            }
        )
        if key is not UNSET:
            field_dict["key"] = key
        if guard_id is not UNSET:
            field_dict["guard_id"] = guard_id
        if guard_status is not UNSET:
            field_dict["guard_status"] = guard_status
        if restored_revision is not UNSET:
            field_dict["restored_revision"] = restored_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.config_revision_detail_secret_versions import ConfigRevisionDetailSecretVersions
        from ..models.config_revision_detail_variables import ConfigRevisionDetailVariables

        d = dict(src_dict)
        revision = d.pop("revision")

        scope = d.pop("scope")

        source = check_config_revision_source(d.pop("source"))

        actor = d.pop("actor")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        variable_count = d.pop("variable_count")

        secret_count = d.pop("secret_count")

        variables = ConfigRevisionDetailVariables.from_dict(d.pop("variables"))

        secret_versions = ConfigRevisionDetailSecretVersions.from_dict(d.pop("secret_versions"))

        key = d.pop("key", UNSET)

        _guard_id = d.pop("guard_id", UNSET)
        guard_id: UUID | Unset
        if isinstance(_guard_id, Unset):
            guard_id = UNSET
        else:
            guard_id = UUID(_guard_id)

        guard_status = d.pop("guard_status", UNSET)

        restored_revision = d.pop("restored_revision", UNSET)

        config_revision_detail = cls(
            revision=revision,
            scope=scope,
            source=source,
            actor=actor,
            created_at=created_at,
            variable_count=variable_count,
            secret_count=secret_count,
            variables=variables,
            secret_versions=secret_versions,
            key=key,
            guard_id=guard_id,
            guard_status=guard_status,
            restored_revision=restored_revision,
        )

        config_revision_detail.additional_properties = d
        return config_revision_detail

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
