from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObjectStorageUsageReport")


@_attrs_define
class ObjectStorageUsageReport:
    """Authoritative cumulative provider usage for one account, backend and UTC month. Missing data is not zero; costs are
    EUR millicents.

    """

    account_id: UUID
    backend_id: str
    backend_fingerprint: str
    source: str
    period_start: datetime.datetime
    observed_at: datetime.datetime
    stored_byte_hours: int
    request_count: int
    egress_bytes: int
    cost_millicents: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = str(self.account_id)

        backend_id = self.backend_id

        backend_fingerprint = self.backend_fingerprint

        source = self.source

        period_start = self.period_start.isoformat()

        observed_at = self.observed_at.isoformat()

        stored_byte_hours = self.stored_byte_hours

        request_count = self.request_count

        egress_bytes = self.egress_bytes

        cost_millicents = self.cost_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "backend_id": backend_id,
                "backend_fingerprint": backend_fingerprint,
                "source": source,
                "period_start": period_start,
                "observed_at": observed_at,
                "stored_byte_hours": stored_byte_hours,
                "request_count": request_count,
                "egress_bytes": egress_bytes,
                "cost_millicents": cost_millicents,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        backend_id = d.pop("backend_id")

        backend_fingerprint = d.pop("backend_fingerprint")

        source = d.pop("source")

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        stored_byte_hours = d.pop("stored_byte_hours")

        request_count = d.pop("request_count")

        egress_bytes = d.pop("egress_bytes")

        cost_millicents = d.pop("cost_millicents")

        object_storage_usage_report = cls(
            account_id=account_id,
            backend_id=backend_id,
            backend_fingerprint=backend_fingerprint,
            source=source,
            period_start=period_start,
            observed_at=observed_at,
            stored_byte_hours=stored_byte_hours,
            request_count=request_count,
            egress_bytes=egress_bytes,
            cost_millicents=cost_millicents,
        )

        object_storage_usage_report.additional_properties = d
        return object_storage_usage_report

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
