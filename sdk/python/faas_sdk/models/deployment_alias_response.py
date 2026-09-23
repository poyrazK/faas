from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DeploymentAliasResponse")


@_attrs_define
class DeploymentAliasResponse:
    """A customer's named pointer to one per-app deployment revision, including its stable host when the platform apps
    domain is configured.

    """

    name: str
    deployment_id: UUID
    revision: int
    """Per-app revision number; rendered in CLI output as vN."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    host: str | Unset = UNSET
    """Stable one-label hostname for this alias. It is keyed by an immutable app identifier and remains stable if
    the app slug is renamed."""
    url: str | Unset = UNSET
    """HTTPS URL for the alias host; omitted when the platform apps domain is not configured."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        deployment_id = str(self.deployment_id)

        revision = self.revision

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        host = self.host

        url = self.url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "deployment_id": deployment_id,
                "revision": revision,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if host is not UNSET:
            field_dict["host"] = host
        if url is not UNSET:
            field_dict["url"] = url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        deployment_id = UUID(d.pop("deployment_id"))

        revision = d.pop("revision")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        host = d.pop("host", UNSET)

        url = d.pop("url", UNSET)

        deployment_alias_response = cls(
            name=name,
            deployment_id=deployment_id,
            revision=revision,
            created_at=created_at,
            updated_at=updated_at,
            host=host,
            url=url,
        )

        deployment_alias_response.additional_properties = d
        return deployment_alias_response

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
