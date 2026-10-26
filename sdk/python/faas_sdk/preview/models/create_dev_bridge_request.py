from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateDevBridgeRequest")


@_attrs_define
class CreateDevBridgeRequest:
    """Owned app and explicit development graph for local execution."""

    app: str
    environment: str
    developer_id: str
    dependencies: list[str] | Unset = UNSET
    entrypoint: str | Unset = UNSET
    """Remote frontend included in the selected graph and used for the session URL."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        environment = self.environment

        developer_id = self.developer_id

        dependencies: list[str] | Unset = UNSET
        if not isinstance(self.dependencies, Unset):
            dependencies = self.dependencies

        entrypoint = self.entrypoint

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "environment": environment,
                "developer_id": developer_id,
            }
        )
        if dependencies is not UNSET:
            field_dict["dependencies"] = dependencies
        if entrypoint is not UNSET:
            field_dict["entrypoint"] = entrypoint

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app = d.pop("app")

        environment = d.pop("environment")

        developer_id = d.pop("developer_id")

        dependencies = cast(list[str], d.pop("dependencies", UNSET))

        entrypoint = d.pop("entrypoint", UNSET)

        create_dev_bridge_request = cls(
            app=app,
            environment=environment,
            developer_id=developer_id,
            dependencies=dependencies,
            entrypoint=entrypoint,
        )

        create_dev_bridge_request.additional_properties = d
        return create_dev_bridge_request

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
