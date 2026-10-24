from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OutboundIntegrationUsageResponse")


@_attrs_define
class OutboundIntegrationUsageResponse:
    """Current UTC-day admitted-request count and the effective optional request limit."""

    daily_request_count: int
    daily_request_limit: int | None
    usage_date: datetime.date
    resets_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        daily_request_count = self.daily_request_count

        daily_request_limit: int | None
        daily_request_limit = self.daily_request_limit

        usage_date = self.usage_date.isoformat()

        resets_at = self.resets_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "daily_request_count": daily_request_count,
                "daily_request_limit": daily_request_limit,
                "usage_date": usage_date,
                "resets_at": resets_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        daily_request_count = d.pop("daily_request_count")

        def _parse_daily_request_limit(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        daily_request_limit = _parse_daily_request_limit(d.pop("daily_request_limit"))

        usage_date = datetime.date.fromisoformat(d.pop("usage_date"))

        resets_at = datetime.datetime.fromisoformat(d.pop("resets_at"))

        outbound_integration_usage_response = cls(
            daily_request_count=daily_request_count,
            daily_request_limit=daily_request_limit,
            usage_date=usage_date,
            resets_at=resets_at,
        )

        outbound_integration_usage_response.additional_properties = d
        return outbound_integration_usage_response

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
