from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="RequestEventExecutionLineageCaseBackfillRequest")


@_attrs_define
class RequestEventExecutionLineageCaseBackfillRequest:
    """Idempotent request identity and previous-scan guard for a bounded diagnostic backfill."""

    request_id: UUID
    expected_previous_request_id: UUID | Unset = UNSET
    """Omit for the first request; otherwise use the latest completed, cancelled or expired request UUID. Repeated
    submissions preserve their original guard."""

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        expected_previous_request_id: str | Unset = UNSET
        if not isinstance(self.expected_previous_request_id, Unset):
            expected_previous_request_id = str(self.expected_previous_request_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
            }
        )
        if expected_previous_request_id is not UNSET:
            field_dict["expected_previous_request_id"] = expected_previous_request_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        _expected_previous_request_id = d.pop("expected_previous_request_id", UNSET)
        expected_previous_request_id: UUID | Unset
        if isinstance(_expected_previous_request_id, Unset):
            expected_previous_request_id = UNSET
        else:
            expected_previous_request_id = UUID(_expected_previous_request_id)

        request_event_execution_lineage_case_backfill_request = cls(
            request_id=request_id,
            expected_previous_request_id=expected_previous_request_id,
        )

        return request_event_execution_lineage_case_backfill_request
