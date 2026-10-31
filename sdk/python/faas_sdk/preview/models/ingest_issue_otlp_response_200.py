from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="IngestIssueOTLPResponse200")


@_attrs_define
class IngestIssueOTLPResponse200:
    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        ingest_issue_otlp_response_200 = cls()

        return ingest_issue_otlp_response_200
