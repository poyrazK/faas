from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tool_registry_tool_access_mode import ToolRegistryToolAccessMode, check_tool_registry_tool_access_mode
from ..models.tool_registry_tool_availability import ToolRegistryToolAvailability, check_tool_registry_tool_availability
from ..models.tool_registry_tool_evidence_origin import (
    ToolRegistryToolEvidenceOrigin,
    check_tool_registry_tool_evidence_origin,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.tool_grant_access import ToolGrantAccess


T = TypeVar("T", bound="ToolRegistryTool")


@_attrs_define
class ToolRegistryTool:
    """Single-tool catalog projection excluding sibling tools, prompts, resources, test names and credentials. Projected
    and full publication digests describe different bytes.

    """

    version: str
    account_id: str
    app: str
    app_id: str
    name: str
    deployment_id: str
    source_sha256: str
    deployment_source_sha256: str
    catalog_sha256: str
    """Original runtime catalog pin"""
    catalog_contract_sha256: str
    """Digest of the returned single-tool projection"""
    published_catalog_contract_sha256: str
    """Digest of the complete archived publication contract"""
    catalog_base64: str
    access_mode: ToolRegistryToolAccessMode
    required_scopes: list[str]
    evidence_origin: ToolRegistryToolEvidenceOrigin
    availability: ToolRegistryToolAvailability
    grants: list[ToolGrantAccess] | Unset = UNSET
    """Active grants for the exact requested tool and current key; never a dispatch receipt"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        account_id = self.account_id

        app = self.app

        app_id = self.app_id

        name = self.name

        deployment_id = self.deployment_id

        source_sha256 = self.source_sha256

        deployment_source_sha256 = self.deployment_source_sha256

        catalog_sha256 = self.catalog_sha256

        catalog_contract_sha256 = self.catalog_contract_sha256

        published_catalog_contract_sha256 = self.published_catalog_contract_sha256

        catalog_base64 = self.catalog_base64

        access_mode: str = self.access_mode

        required_scopes = self.required_scopes

        evidence_origin: str = self.evidence_origin

        availability: str = self.availability

        grants: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.grants, Unset):
            grants = []
            for grants_item_data in self.grants:
                grants_item = grants_item_data.to_dict()
                grants.append(grants_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "account_id": account_id,
                "app": app,
                "app_id": app_id,
                "name": name,
                "deployment_id": deployment_id,
                "source_sha256": source_sha256,
                "deployment_source_sha256": deployment_source_sha256,
                "catalog_sha256": catalog_sha256,
                "catalog_contract_sha256": catalog_contract_sha256,
                "published_catalog_contract_sha256": published_catalog_contract_sha256,
                "catalog_base64": catalog_base64,
                "access_mode": access_mode,
                "required_scopes": required_scopes,
                "evidence_origin": evidence_origin,
                "availability": availability,
            }
        )
        if grants is not UNSET:
            field_dict["grants"] = grants

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tool_grant_access import ToolGrantAccess

        d = dict(src_dict)
        version = d.pop("version")

        account_id = d.pop("account_id")

        app = d.pop("app")

        app_id = d.pop("app_id")

        name = d.pop("name")

        deployment_id = d.pop("deployment_id")

        source_sha256 = d.pop("source_sha256")

        deployment_source_sha256 = d.pop("deployment_source_sha256")

        catalog_sha256 = d.pop("catalog_sha256")

        catalog_contract_sha256 = d.pop("catalog_contract_sha256")

        published_catalog_contract_sha256 = d.pop("published_catalog_contract_sha256")

        catalog_base64 = d.pop("catalog_base64")

        access_mode = check_tool_registry_tool_access_mode(d.pop("access_mode"))

        required_scopes = cast(list[str], d.pop("required_scopes"))

        evidence_origin = check_tool_registry_tool_evidence_origin(d.pop("evidence_origin"))

        availability = check_tool_registry_tool_availability(d.pop("availability"))

        _grants = d.pop("grants", UNSET)
        grants: list[ToolGrantAccess] | Unset = UNSET
        if _grants is not UNSET:
            grants = []
            for grants_item_data in _grants:
                grants_item = ToolGrantAccess.from_dict(grants_item_data)

                grants.append(grants_item)

        tool_registry_tool = cls(
            version=version,
            account_id=account_id,
            app=app,
            app_id=app_id,
            name=name,
            deployment_id=deployment_id,
            source_sha256=source_sha256,
            deployment_source_sha256=deployment_source_sha256,
            catalog_sha256=catalog_sha256,
            catalog_contract_sha256=catalog_contract_sha256,
            published_catalog_contract_sha256=published_catalog_contract_sha256,
            catalog_base64=catalog_base64,
            access_mode=access_mode,
            required_scopes=required_scopes,
            evidence_origin=evidence_origin,
            availability=availability,
            grants=grants,
        )

        tool_registry_tool.additional_properties = d
        return tool_registry_tool

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
