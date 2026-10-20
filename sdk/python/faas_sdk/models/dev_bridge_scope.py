from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DevBridgeScope")


@_attrs_define
class DevBridgeScope:
    """Server-resolved account, developer, environment and permitted apps."""

    account_id: UUID
    developer_id: str
    project_id: UUID
    environment_id: UUID
    target_app_id: UUID
    dependency_app_ids: list[UUID] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = str(self.account_id)

        developer_id = self.developer_id

        project_id = str(self.project_id)

        environment_id = str(self.environment_id)

        target_app_id = str(self.target_app_id)

        dependency_app_ids: list[str] | Unset = UNSET
        if not isinstance(self.dependency_app_ids, Unset):
            dependency_app_ids = []
            for dependency_app_ids_item_data in self.dependency_app_ids:
                dependency_app_ids_item = str(dependency_app_ids_item_data)
                dependency_app_ids.append(dependency_app_ids_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "developer_id": developer_id,
                "project_id": project_id,
                "environment_id": environment_id,
                "target_app_id": target_app_id,
            }
        )
        if dependency_app_ids is not UNSET:
            field_dict["dependency_app_ids"] = dependency_app_ids

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        developer_id = d.pop("developer_id")

        project_id = UUID(d.pop("project_id"))

        environment_id = UUID(d.pop("environment_id"))

        target_app_id = UUID(d.pop("target_app_id"))

        _dependency_app_ids = d.pop("dependency_app_ids", UNSET)
        dependency_app_ids: list[UUID] | Unset = UNSET
        if _dependency_app_ids is not UNSET:
            dependency_app_ids = []
            for dependency_app_ids_item_data in _dependency_app_ids:
                dependency_app_ids_item = UUID(dependency_app_ids_item_data)

                dependency_app_ids.append(dependency_app_ids_item)

        dev_bridge_scope = cls(
            account_id=account_id,
            developer_id=developer_id,
            project_id=project_id,
            environment_id=environment_id,
            target_app_id=target_app_id,
            dependency_app_ids=dependency_app_ids,
        )

        dev_bridge_scope.additional_properties = d
        return dev_bridge_scope

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
