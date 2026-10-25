from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.dev_bridge_credentials import DevBridgeCredentials
    from ..models.dev_bridge_dependency import DevBridgeDependency
    from ..models.dev_bridge_session import DevBridgeSession


T = TypeVar("T", bound="CreateDevBridgeResponse")


@_attrs_define
class CreateDevBridgeResponse:
    """New development lease with credentials returned exactly once."""

    session: DevBridgeSession
    """Durable session metadata with credential digests excluded."""
    credentials: DevBridgeCredentials
    """Distinct secrets for laptop attachment and request routing."""
    environment_url: str
    dependencies: list[DevBridgeDependency] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        session = self.session.to_dict()

        credentials = self.credentials.to_dict()

        environment_url = self.environment_url

        dependencies: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.dependencies, Unset):
            dependencies = []
            for dependencies_item_data in self.dependencies:
                dependencies_item = dependencies_item_data.to_dict()
                dependencies.append(dependencies_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "session": session,
                "credentials": credentials,
                "environment_url": environment_url,
            }
        )
        if dependencies is not UNSET:
            field_dict["dependencies"] = dependencies

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dev_bridge_credentials import DevBridgeCredentials
        from ..models.dev_bridge_dependency import DevBridgeDependency
        from ..models.dev_bridge_session import DevBridgeSession

        d = dict(src_dict)
        session = DevBridgeSession.from_dict(d.pop("session"))

        credentials = DevBridgeCredentials.from_dict(d.pop("credentials"))

        environment_url = d.pop("environment_url")

        _dependencies = d.pop("dependencies", UNSET)
        dependencies: list[DevBridgeDependency] | Unset = UNSET
        if _dependencies is not UNSET:
            dependencies = []
            for dependencies_item_data in _dependencies:
                dependencies_item = DevBridgeDependency.from_dict(dependencies_item_data)

                dependencies.append(dependencies_item)

        create_dev_bridge_response = cls(
            session=session,
            credentials=credentials,
            environment_url=environment_url,
            dependencies=dependencies,
        )

        create_dev_bridge_response.additional_properties = d
        return create_dev_bridge_response

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
