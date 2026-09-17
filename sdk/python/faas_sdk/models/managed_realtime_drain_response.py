from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_realtime_drain_response_status import (
    ManagedRealtimeDrainResponseStatus,
    check_managed_realtime_drain_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_realtime_drain_result import ManagedRealtimeDrainResult


T = TypeVar("T", bound="ManagedRealtimeDrainResponse")


@_attrs_define
class ManagedRealtimeDrainResponse:
    """Bounded, auditable result of a realtime connection drain."""

    operation_id: UUID
    """Durable identifier for this drain operation."""
    status: ManagedRealtimeDrainResponseStatus
    """Durable operation state. A partial operation had at least one gone or failed connection."""
    created_at: datetime.datetime
    results: list[ManagedRealtimeDrainResult]
    matched: int
    """Number selected after applying the limit."""
    closed: int
    gone: int
    """Selected connections that disappeared before close."""
    failed: int
    limit: int
    truncated: bool
    """True when more eligible connections existed than the limit."""
    dry_run: bool
    partial: bool
    """Indicates that the inventory did not cover every active realtime node."""
    nodes_queried: int
    nodes_unavailable: int
    completed_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        status: str = self.status

        created_at = self.created_at.isoformat()

        results = []
        for results_item_data in self.results:
            results_item = results_item_data.to_dict()
            results.append(results_item)

        matched = self.matched

        closed = self.closed

        gone = self.gone

        failed = self.failed

        limit = self.limit

        truncated = self.truncated

        dry_run = self.dry_run

        partial = self.partial

        nodes_queried = self.nodes_queried

        nodes_unavailable = self.nodes_unavailable

        completed_at: None | str | Unset
        if isinstance(self.completed_at, Unset):
            completed_at = UNSET
        elif isinstance(self.completed_at, datetime.datetime):
            completed_at = self.completed_at.isoformat()
        else:
            completed_at = self.completed_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "operation_id": operation_id,
                "status": status,
                "created_at": created_at,
                "results": results,
                "matched": matched,
                "closed": closed,
                "gone": gone,
                "failed": failed,
                "limit": limit,
                "truncated": truncated,
                "dry_run": dry_run,
                "partial": partial,
                "nodes_queried": nodes_queried,
                "nodes_unavailable": nodes_unavailable,
            }
        )
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_drain_result import ManagedRealtimeDrainResult

        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        status = check_managed_realtime_drain_response_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        results = []
        _results = d.pop("results")
        for results_item_data in _results:
            results_item = ManagedRealtimeDrainResult.from_dict(results_item_data)

            results.append(results_item)

        matched = d.pop("matched")

        closed = d.pop("closed")

        gone = d.pop("gone")

        failed = d.pop("failed")

        limit = d.pop("limit")

        truncated = d.pop("truncated")

        dry_run = d.pop("dry_run")

        partial = d.pop("partial")

        nodes_queried = d.pop("nodes_queried")

        nodes_unavailable = d.pop("nodes_unavailable")

        def _parse_completed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                completed_at_type_0 = datetime.datetime.fromisoformat(data)

                return completed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        completed_at = _parse_completed_at(d.pop("completed_at", UNSET))

        managed_realtime_drain_response = cls(
            operation_id=operation_id,
            status=status,
            created_at=created_at,
            results=results,
            matched=matched,
            closed=closed,
            gone=gone,
            failed=failed,
            limit=limit,
            truncated=truncated,
            dry_run=dry_run,
            partial=partial,
            nodes_queried=nodes_queried,
            nodes_unavailable=nodes_unavailable,
            completed_at=completed_at,
        )

        managed_realtime_drain_response.additional_properties = d
        return managed_realtime_drain_response

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
