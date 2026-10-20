from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformTenantUsageBucketResponse")


@_attrs_define
class PlatformTenantUsageBucketResponse:
    """One UTC day of durable tenant-attributed usage. Exactly one of consumer_id, surface_id, or jwt_authorization_rule_id
    is present.

    """

    app_id: UUID
    window_start: datetime.datetime
    request_count: int
    error_count: int
    billable_units: int
    consumer_id: UUID | Unset = UNSET
    surface_id: UUID | Unset = UNSET
    jwt_authorization_rule_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        window_start = self.window_start.isoformat()

        request_count = self.request_count

        error_count = self.error_count

        billable_units = self.billable_units

        consumer_id: str | Unset = UNSET
        if not isinstance(self.consumer_id, Unset):
            consumer_id = str(self.consumer_id)

        surface_id: str | Unset = UNSET
        if not isinstance(self.surface_id, Unset):
            surface_id = str(self.surface_id)

        jwt_authorization_rule_id: str | Unset = UNSET
        if not isinstance(self.jwt_authorization_rule_id, Unset):
            jwt_authorization_rule_id = str(self.jwt_authorization_rule_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "window_start": window_start,
                "request_count": request_count,
                "error_count": error_count,
                "billable_units": billable_units,
            }
        )
        if consumer_id is not UNSET:
            field_dict["consumer_id"] = consumer_id
        if surface_id is not UNSET:
            field_dict["surface_id"] = surface_id
        if jwt_authorization_rule_id is not UNSET:
            field_dict["jwt_authorization_rule_id"] = jwt_authorization_rule_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        request_count = d.pop("request_count")

        error_count = d.pop("error_count")

        billable_units = d.pop("billable_units")

        _consumer_id = d.pop("consumer_id", UNSET)
        consumer_id: UUID | Unset
        if isinstance(_consumer_id, Unset):
            consumer_id = UNSET
        else:
            consumer_id = UUID(_consumer_id)

        _surface_id = d.pop("surface_id", UNSET)
        surface_id: UUID | Unset
        if isinstance(_surface_id, Unset):
            surface_id = UNSET
        else:
            surface_id = UUID(_surface_id)

        _jwt_authorization_rule_id = d.pop("jwt_authorization_rule_id", UNSET)
        jwt_authorization_rule_id: UUID | Unset
        if isinstance(_jwt_authorization_rule_id, Unset):
            jwt_authorization_rule_id = UNSET
        else:
            jwt_authorization_rule_id = UUID(_jwt_authorization_rule_id)

        platform_tenant_usage_bucket_response = cls(
            app_id=app_id,
            window_start=window_start,
            request_count=request_count,
            error_count=error_count,
            billable_units=billable_units,
            consumer_id=consumer_id,
            surface_id=surface_id,
            jwt_authorization_rule_id=jwt_authorization_rule_id,
        )

        platform_tenant_usage_bucket_response.additional_properties = d
        return platform_tenant_usage_bucket_response

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
