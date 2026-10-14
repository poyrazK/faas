from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="RetryEventExecutionLineageCaseRequest")


@_attrs_define
class RetryEventExecutionLineageCaseRequest:
    """Idempotent reevaluation identity and reviewed revision guard for a single lineage case."""

    request_id: UUID
    expected_revision: int
    expected_diagnostic_id: UUID | Unset = UNSET
    """Required when the reviewed case has diagnostic_id; omit for legacy cases. Context: Retry Event Execution
    Lineage Case Request."""

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        expected_revision = self.expected_revision

        expected_diagnostic_id: str | Unset = UNSET
        if not isinstance(self.expected_diagnostic_id, Unset):
            expected_diagnostic_id = str(self.expected_diagnostic_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "expected_revision": expected_revision,
            }
        )
        if expected_diagnostic_id is not UNSET:
            field_dict["expected_diagnostic_id"] = expected_diagnostic_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        expected_revision = d.pop("expected_revision")

        _expected_diagnostic_id = d.pop("expected_diagnostic_id", UNSET)
        expected_diagnostic_id: UUID | Unset
        if isinstance(_expected_diagnostic_id, Unset):
            expected_diagnostic_id = UNSET
        else:
            expected_diagnostic_id = UUID(_expected_diagnostic_id)

        retry_event_execution_lineage_case_request = cls(
            request_id=request_id,
            expected_revision=expected_revision,
            expected_diagnostic_id=expected_diagnostic_id,
        )

        return retry_event_execution_lineage_case_request
