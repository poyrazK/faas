from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateOutboundAIAccessTokenRequest")


@_attrs_define
class CreateOutboundAIAccessTokenRequest:
    """Issue an access token for an app. expires_in_hours defaults to 168 (7 days). integration_ids narrows the token to
    listed AI integrations bound to the app; empty allows every AI integration bound to the app and the account's
    routes.

    """

    name: str
    expires_in_hours: int | Unset = UNSET
    integration_ids: list[UUID] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        expires_in_hours = self.expires_in_hours

        integration_ids: list[str] | Unset = UNSET
        if not isinstance(self.integration_ids, Unset):
            integration_ids = []
            for integration_ids_item_data in self.integration_ids:
                integration_ids_item = str(integration_ids_item_data)
                integration_ids.append(integration_ids_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
            }
        )
        if expires_in_hours is not UNSET:
            field_dict["expires_in_hours"] = expires_in_hours
        if integration_ids is not UNSET:
            field_dict["integration_ids"] = integration_ids

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        expires_in_hours = d.pop("expires_in_hours", UNSET)

        _integration_ids = d.pop("integration_ids", UNSET)
        integration_ids: list[UUID] | Unset = UNSET
        if _integration_ids is not UNSET:
            integration_ids = []
            for integration_ids_item_data in _integration_ids:
                integration_ids_item = UUID(integration_ids_item_data)

                integration_ids.append(integration_ids_item)

        create_outbound_ai_access_token_request = cls(
            name=name,
            expires_in_hours=expires_in_hours,
            integration_ids=integration_ids,
        )

        create_outbound_ai_access_token_request.additional_properties = d
        return create_outbound_ai_access_token_request

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
