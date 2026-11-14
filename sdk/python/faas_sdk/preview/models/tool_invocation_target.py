from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ToolInvocationTarget")


@_attrs_define
class ToolInvocationTarget:
    """Immutable native target resolved from the exact owned publication and deployment source artifact."""

    app: str
    app_id: str
    deployment_id: str
    deployment_source_sha256: str
    catalog_sha256: str
    catalog_contract_sha256: str
    endpoint_path: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        app_id = self.app_id

        deployment_id = self.deployment_id

        deployment_source_sha256 = self.deployment_source_sha256

        catalog_sha256 = self.catalog_sha256

        catalog_contract_sha256 = self.catalog_contract_sha256

        endpoint_path = self.endpoint_path

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "deployment_source_sha256": deployment_source_sha256,
                "catalog_sha256": catalog_sha256,
                "catalog_contract_sha256": catalog_contract_sha256,
                "endpoint_path": endpoint_path,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app = d.pop("app")

        app_id = d.pop("app_id")

        deployment_id = d.pop("deployment_id")

        deployment_source_sha256 = d.pop("deployment_source_sha256")

        catalog_sha256 = d.pop("catalog_sha256")

        catalog_contract_sha256 = d.pop("catalog_contract_sha256")

        endpoint_path = d.pop("endpoint_path")

        tool_invocation_target = cls(
            app=app,
            app_id=app_id,
            deployment_id=deployment_id,
            deployment_source_sha256=deployment_source_sha256,
            catalog_sha256=catalog_sha256,
            catalog_contract_sha256=catalog_contract_sha256,
            endpoint_path=endpoint_path,
        )

        tool_invocation_target.additional_properties = d
        return tool_invocation_target

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
