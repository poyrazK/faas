from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_consumer_usage_waiver_request import APIConsumerUsageWaiverRequest


T = TypeVar("T", bound="CreateAPIConsumerUsageStatementRequest")


@_attrs_define
class CreateAPIConsumerUsageStatementRequest:
    """Explicit UTC-minute period to snapshot as an immutable usage statement."""

    period_start: datetime.datetime
    period_end: datetime.datetime
    waiver: APIConsumerUsageWaiverRequest | Unset = UNSET
    """The exact unpriced model usage selected for administrative approval on an app consumer draft."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        waiver: dict[str, Any] | Unset = UNSET
        if not isinstance(self.waiver, Unset):
            waiver = self.waiver.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "period_start": period_start,
                "period_end": period_end,
            }
        )
        if waiver is not UNSET:
            field_dict["waiver"] = waiver

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_usage_waiver_request import APIConsumerUsageWaiverRequest

        d = dict(src_dict)
        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        _waiver = d.pop("waiver", UNSET)
        waiver: APIConsumerUsageWaiverRequest | Unset
        if isinstance(_waiver, Unset):
            waiver = UNSET
        else:
            waiver = APIConsumerUsageWaiverRequest.from_dict(_waiver)

        create_api_consumer_usage_statement_request = cls(
            period_start=period_start,
            period_end=period_end,
            waiver=waiver,
        )

        create_api_consumer_usage_statement_request.additional_properties = d
        return create_api_consumer_usage_statement_request

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
