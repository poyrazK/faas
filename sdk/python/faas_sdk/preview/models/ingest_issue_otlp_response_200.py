from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.ingest_issue_otlp_response_200_partial_success import IngestIssueOTLPResponse200PartialSuccess


T = TypeVar("T", bound="IngestIssueOTLPResponse200")


@_attrs_define
class IngestIssueOTLPResponse200:
    partial_success: IngestIssueOTLPResponse200PartialSuccess | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        partial_success: dict[str, Any] | Unset = UNSET
        if not isinstance(self.partial_success, Unset):
            partial_success = self.partial_success.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if partial_success is not UNSET:
            field_dict["partialSuccess"] = partial_success

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.ingest_issue_otlp_response_200_partial_success import IngestIssueOTLPResponse200PartialSuccess

        d = dict(src_dict)
        _partial_success = d.pop("partialSuccess", UNSET)
        partial_success: IngestIssueOTLPResponse200PartialSuccess | Unset
        if isinstance(_partial_success, Unset):
            partial_success = UNSET
        else:
            partial_success = IngestIssueOTLPResponse200PartialSuccess.from_dict(_partial_success)

        ingest_issue_otlp_response_200 = cls(
            partial_success=partial_success,
        )

        return ingest_issue_otlp_response_200
