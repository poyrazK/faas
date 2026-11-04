from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteCustomerObservation")


@_attrs_define
class RouteCustomerObservation:
    """One request-time consumer and tenant identity group; either or both IDs may be present."""

    requests: int
    """Weighted retained requests attributed to this identity pair."""
    last_observed_at: datetime.datetime
    """Latest recorded telemetry timestamp for this identity group; may be a minute bucket."""
    consumer_id: UUID | Unset = UNSET
    """Stable authenticated consumer ID owned by the selected app, if recorded and resolvable."""
    platform_tenant_id: UUID | Unset = UNSET
    """Account-owned tenant ID recorded when these requests occurred, if resolvable."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        requests = self.requests

        last_observed_at = self.last_observed_at.isoformat()

        consumer_id: str | Unset = UNSET
        if not isinstance(self.consumer_id, Unset):
            consumer_id = str(self.consumer_id)

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "requests": requests,
                "last_observed_at": last_observed_at,
            }
        )
        if consumer_id is not UNSET:
            field_dict["consumer_id"] = consumer_id
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        requests = d.pop("requests")

        last_observed_at = datetime.datetime.fromisoformat(d.pop("last_observed_at"))

        _consumer_id = d.pop("consumer_id", UNSET)
        consumer_id: UUID | Unset
        if isinstance(_consumer_id, Unset):
            consumer_id = UNSET
        else:
            consumer_id = UUID(_consumer_id)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        route_customer_observation = cls(
            requests=requests,
            last_observed_at=last_observed_at,
            consumer_id=consumer_id,
            platform_tenant_id=platform_tenant_id,
        )

        route_customer_observation.additional_properties = d
        return route_customer_observation

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
