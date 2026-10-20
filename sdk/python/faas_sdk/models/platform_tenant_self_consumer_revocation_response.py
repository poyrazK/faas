from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.platform_tenant_self_consumer_response import PlatformTenantSelfConsumerResponse


T = TypeVar("T", bound="PlatformTenantSelfConsumerRevocationResponse")


@_attrs_define
class PlatformTenantSelfConsumerRevocationResponse:
    """Revoked identities with the number of active keys newly revoked by this operation; exact retries return zero."""

    consumers: list[PlatformTenantSelfConsumerResponse]
    revoked_keys: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        consumers = []
        for consumers_item_data in self.consumers:
            consumers_item = consumers_item_data.to_dict()
            consumers.append(consumers_item)

        revoked_keys = self.revoked_keys

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "consumers": consumers,
                "revoked_keys": revoked_keys,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_self_consumer_response import PlatformTenantSelfConsumerResponse

        d = dict(src_dict)
        consumers = []
        _consumers = d.pop("consumers")
        for consumers_item_data in _consumers:
            consumers_item = PlatformTenantSelfConsumerResponse.from_dict(consumers_item_data)

            consumers.append(consumers_item)

        revoked_keys = d.pop("revoked_keys")

        platform_tenant_self_consumer_revocation_response = cls(
            consumers=consumers,
            revoked_keys=revoked_keys,
        )

        platform_tenant_self_consumer_revocation_response.additional_properties = d
        return platform_tenant_self_consumer_revocation_response

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
