from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_self_consumer_response_status import (
    PlatformTenantSelfConsumerResponseStatus,
    check_platform_tenant_self_consumer_response_status,
)

T = TypeVar("T", bound="PlatformTenantSelfConsumerResponse")


@_attrs_define
class PlatformTenantSelfConsumerResponse:
    """Minimal linked-consumer identity used to target tenant-managed credentials; app IDs and account metadata are
    omitted.

    """

    consumer_id: UUID
    external_ref: str
    name: str
    status: PlatformTenantSelfConsumerResponseStatus
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        consumer_id = str(self.consumer_id)

        external_ref = self.external_ref

        name = self.name

        status: str = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "consumer_id": consumer_id,
                "external_ref": external_ref,
                "name": name,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        consumer_id = UUID(d.pop("consumer_id"))

        external_ref = d.pop("external_ref")

        name = d.pop("name")

        status = check_platform_tenant_self_consumer_response_status(d.pop("status"))

        platform_tenant_self_consumer_response = cls(
            consumer_id=consumer_id,
            external_ref=external_ref,
            name=name,
            status=status,
        )

        platform_tenant_self_consumer_response.additional_properties = d
        return platform_tenant_self_consumer_response

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
