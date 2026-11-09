from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="IngestIssueOTLPResponse200PartialSuccess")


@_attrs_define
class IngestIssueOTLPResponse200PartialSuccess:
    rejected_log_records: str | Unset = UNSET
    """Decimal count of rejected log records."""
    rejected_spans: str | Unset = UNSET
    """Decimal count of rejected spans."""
    error_message: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rejected_log_records = self.rejected_log_records

        rejected_spans = self.rejected_spans

        error_message = self.error_message

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if rejected_log_records is not UNSET:
            field_dict["rejectedLogRecords"] = rejected_log_records
        if rejected_spans is not UNSET:
            field_dict["rejectedSpans"] = rejected_spans
        if error_message is not UNSET:
            field_dict["errorMessage"] = error_message

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rejected_log_records = d.pop("rejectedLogRecords", UNSET)

        rejected_spans = d.pop("rejectedSpans", UNSET)

        error_message = d.pop("errorMessage", UNSET)

        ingest_issue_otlp_response_200_partial_success = cls(
            rejected_log_records=rejected_log_records,
            rejected_spans=rejected_spans,
            error_message=error_message,
        )

        ingest_issue_otlp_response_200_partial_success.additional_properties = d
        return ingest_issue_otlp_response_200_partial_success

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
