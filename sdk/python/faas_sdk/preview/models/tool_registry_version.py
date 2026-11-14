from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tool_registry_version_access_mode import (
    ToolRegistryVersionAccessMode,
    check_tool_registry_version_access_mode,
)
from ..models.tool_registry_version_availability import (
    ToolRegistryVersionAvailability,
    check_tool_registry_version_availability,
)
from ..models.tool_registry_version_evidence_origin import (
    ToolRegistryVersionEvidenceOrigin,
    check_tool_registry_version_evidence_origin,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ToolRegistryVersion")


@_attrs_define
class ToolRegistryVersion:
    """Completed publisher-attested tool version and access requirements; current availability is not inferred from
    history.

    """

    version: str
    account_id: UUID
    app: str
    tools: list[str]
    deployment_id: str
    source_sha256: str
    deployment_source_sha256: str
    catalog_sha256: str
    evidence_origin: ToolRegistryVersionEvidenceOrigin
    access_mode: ToolRegistryVersionAccessMode
    availability: ToolRegistryVersionAvailability
    app_id: str | Unset = UNSET
    """Immutable app identity when available"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        account_id = str(self.account_id)

        app = self.app

        tools = self.tools

        deployment_id = self.deployment_id

        source_sha256 = self.source_sha256

        deployment_source_sha256 = self.deployment_source_sha256

        catalog_sha256 = self.catalog_sha256

        evidence_origin: str = self.evidence_origin

        access_mode: str = self.access_mode

        availability: str = self.availability

        app_id = self.app_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "account_id": account_id,
                "app": app,
                "tools": tools,
                "deployment_id": deployment_id,
                "source_sha256": source_sha256,
                "deployment_source_sha256": deployment_source_sha256,
                "catalog_sha256": catalog_sha256,
                "evidence_origin": evidence_origin,
                "access_mode": access_mode,
                "availability": availability,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = d.pop("version")

        account_id = UUID(d.pop("account_id"))

        app = d.pop("app")

        tools = cast(list[str], d.pop("tools"))

        deployment_id = d.pop("deployment_id")

        source_sha256 = d.pop("source_sha256")

        deployment_source_sha256 = d.pop("deployment_source_sha256")

        catalog_sha256 = d.pop("catalog_sha256")

        evidence_origin = check_tool_registry_version_evidence_origin(d.pop("evidence_origin"))

        access_mode = check_tool_registry_version_access_mode(d.pop("access_mode"))

        availability = check_tool_registry_version_availability(d.pop("availability"))

        app_id = d.pop("app_id", UNSET)

        tool_registry_version = cls(
            version=version,
            account_id=account_id,
            app=app,
            tools=tools,
            deployment_id=deployment_id,
            source_sha256=source_sha256,
            deployment_source_sha256=deployment_source_sha256,
            catalog_sha256=catalog_sha256,
            evidence_origin=evidence_origin,
            access_mode=access_mode,
            availability=availability,
            app_id=app_id,
        )

        tool_registry_version.additional_properties = d
        return tool_registry_version

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
