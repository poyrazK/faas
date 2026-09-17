from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.managed_realtime_drain_result import ManagedRealtimeDrainResult


T = TypeVar("T", bound="ManagedRealtimeDrainResponse")


@_attrs_define
class ManagedRealtimeDrainResponse:
    """Bounded, auditable result of a realtime connection drain."""

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
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
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

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_drain_result import ManagedRealtimeDrainResult

        d = dict(src_dict)
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

        managed_realtime_drain_response = cls(
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
