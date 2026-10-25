from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.platform_tenant_self_consumer_apply_item_response import PlatformTenantSelfConsumerApplyItemResponse


T = TypeVar("T", bound="ApplyPlatformTenantSelfConsumersResponse")


@_attrs_define
class ApplyPlatformTenantSelfConsumersResponse:
    """Redacted per-surface onboarding results. App and account identifiers are not returned."""

    dry_run: bool
    consumers: list[PlatformTenantSelfConsumerApplyItemResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        dry_run = self.dry_run

        consumers = []
        for consumers_item_data in self.consumers:
            consumers_item = consumers_item_data.to_dict()
            consumers.append(consumers_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "dry_run": dry_run,
                "consumers": consumers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_self_consumer_apply_item_response import (
            PlatformTenantSelfConsumerApplyItemResponse,
        )

        d = dict(src_dict)
        dry_run = d.pop("dry_run")

        consumers = []
        _consumers = d.pop("consumers")
        for consumers_item_data in _consumers:
            consumers_item = PlatformTenantSelfConsumerApplyItemResponse.from_dict(consumers_item_data)

            consumers.append(consumers_item)

        apply_platform_tenant_self_consumers_response = cls(
            dry_run=dry_run,
            consumers=consumers,
        )

        apply_platform_tenant_self_consumers_response.additional_properties = d
        return apply_platform_tenant_self_consumers_response

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
