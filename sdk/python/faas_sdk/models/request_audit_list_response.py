from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.request_audit_record import RequestAuditRecord


T = TypeVar("T", bound="RequestAuditListResponse")


@_attrs_define
class RequestAuditListResponse:
    """Bounded exact gateway-request audit window, newest first."""

    app_id: UUID
    records: list[RequestAuditRecord]
    since: datetime.datetime
    until: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        records = []
        for records_item_data in self.records:
            records_item = records_item_data.to_dict()
            records.append(records_item)

        since = self.since.isoformat()

        until = self.until.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "records": records,
                "since": since,
                "until": until,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.request_audit_record import RequestAuditRecord

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        records = []
        _records = d.pop("records")
        for records_item_data in _records:
            records_item = RequestAuditRecord.from_dict(records_item_data)

            records.append(records_item)

        since = datetime.datetime.fromisoformat(d.pop("since"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        request_audit_list_response = cls(
            app_id=app_id,
            records=records,
            since=since,
            until=until,
        )

        request_audit_list_response.additional_properties = d
        return request_audit_list_response

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
