from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.publish_event_batch_result import PublishEventBatchResult


T = TypeVar("T", bound="DataAPIChangeEventBatchResponse")


@_attrs_define
class DataAPIChangeEventBatchResponse:
    """Per-event outcomes in input order, with the app source the events published under."""

    source: str
    """The app's producer source"""
    results: list[PublishEventBatchResult]

    def to_dict(self) -> dict[str, Any]:
        source = self.source

        results = []
        for results_item_data in self.results:
            results_item = results_item_data.to_dict()
            results.append(results_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source": source,
                "results": results,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.publish_event_batch_result import PublishEventBatchResult

        d = dict(src_dict)
        source = d.pop("source")

        results = []
        _results = d.pop("results")
        for results_item_data in _results:
            results_item = PublishEventBatchResult.from_dict(results_item_data)

            results.append(results_item)

        data_api_change_event_batch_response = cls(
            source=source,
            results=results,
        )

        return data_api_change_event_batch_response
