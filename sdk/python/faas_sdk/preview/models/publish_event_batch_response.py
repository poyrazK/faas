from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.publish_event_batch_result import PublishEventBatchResult


T = TypeVar("T", bound="PublishEventBatchResponse")


@_attrs_define
class PublishEventBatchResponse:
    """Per-event outcomes in the original batch input order."""

    results: list[PublishEventBatchResult]

    def to_dict(self) -> dict[str, Any]:
        results = []
        for results_item_data in self.results:
            results_item = results_item_data.to_dict()
            results.append(results_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "results": results,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.publish_event_batch_result import PublishEventBatchResult

        d = dict(src_dict)
        results = []
        _results = d.pop("results")
        for results_item_data in _results:
            results_item = PublishEventBatchResult.from_dict(results_item_data)

            results.append(results_item)

        publish_event_batch_response = cls(
            results=results,
        )

        return publish_event_batch_response
