from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tool_publication_readiness_role_catalog_format import (
    ToolPublicationReadinessRoleCatalogFormat,
    check_tool_publication_readiness_role_catalog_format,
)
from ..models.tool_publication_readiness_role_protocol_version import (
    ToolPublicationReadinessRoleProtocolVersion,
    check_tool_publication_readiness_role_protocol_version,
)

T = TypeVar("T", bound="ToolPublicationReadinessRole")


@_attrs_define
class ToolPublicationReadinessRole:
    """One required role with its reviewed protocol and canonical catalog identity."""

    name: str
    protocol_version: ToolPublicationReadinessRoleProtocolVersion
    catalog_format: ToolPublicationReadinessRoleCatalogFormat
    catalog_sha256: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        protocol_version: str = self.protocol_version

        catalog_format: int = self.catalog_format

        catalog_sha256 = self.catalog_sha256

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "protocol_version": protocol_version,
                "catalog_format": catalog_format,
                "catalog_sha256": catalog_sha256,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        protocol_version = check_tool_publication_readiness_role_protocol_version(d.pop("protocol_version"))

        catalog_format = check_tool_publication_readiness_role_catalog_format(d.pop("catalog_format"))

        catalog_sha256 = d.pop("catalog_sha256")

        tool_publication_readiness_role = cls(
            name=name,
            protocol_version=protocol_version,
            catalog_format=catalog_format,
            catalog_sha256=catalog_sha256,
        )

        tool_publication_readiness_role.additional_properties = d
        return tool_publication_readiness_role

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
