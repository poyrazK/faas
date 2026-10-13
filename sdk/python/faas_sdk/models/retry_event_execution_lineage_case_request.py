from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="RetryEventExecutionLineageCaseRequest")


@_attrs_define
class RetryEventExecutionLineageCaseRequest:
    request_id: UUID
    expected_revision: int

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        expected_revision = self.expected_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "expected_revision": expected_revision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        expected_revision = d.pop("expected_revision")

        retry_event_execution_lineage_case_request = cls(
            request_id=request_id,
            expected_revision=expected_revision,
        )

        return retry_event_execution_lineage_case_request
