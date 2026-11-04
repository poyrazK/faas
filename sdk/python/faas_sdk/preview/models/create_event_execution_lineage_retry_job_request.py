from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_execution_lineage_retry_selection import EventExecutionLineageRetrySelection


T = TypeVar("T", bound="CreateEventExecutionLineageRetryJobRequest")


@_attrs_define
class CreateEventExecutionLineageRetryJobRequest:
    """Fixed bounded selection and submission UUID for a durable bulk lineage retry job."""

    request_id: UUID
    cases: list[EventExecutionLineageRetrySelection]
    """Unique outbox identities; server normalizes ascending order for idempotency."""

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        cases = []
        for cases_item_data in self.cases:
            cases_item = cases_item_data.to_dict()
            cases.append(cases_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "cases": cases,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_execution_lineage_retry_selection import EventExecutionLineageRetrySelection

        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        cases = []
        _cases = d.pop("cases")
        for cases_item_data in _cases:
            cases_item = EventExecutionLineageRetrySelection.from_dict(cases_item_data)

            cases.append(cases_item)

        create_event_execution_lineage_retry_job_request = cls(
            request_id=request_id,
            cases=cases,
        )

        return create_event_execution_lineage_retry_job_request
