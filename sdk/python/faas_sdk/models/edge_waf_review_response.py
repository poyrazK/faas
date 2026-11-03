from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.edge_waf_review_group import EdgeWAFReviewGroup


T = TypeVar("T", bound="EdgeWAFReviewResponse")


@_attrs_define
class EdgeWAFReviewResponse:
    """An app's sampled kind=waf detections grouped for a false-positive review."""

    since: datetime.datetime
    """Start of the reviewed window"""
    scanned: int
    """Detection events read."""
    truncated: bool
    """The window held more detections than a review reads (5,000 per outcome, newest first), so the oldest were
    left out."""
    groups: list[EdgeWAFReviewGroup]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        since = self.since.isoformat()

        scanned = self.scanned

        truncated = self.truncated

        groups = []
        for groups_item_data in self.groups:
            groups_item = groups_item_data.to_dict()
            groups.append(groups_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "since": since,
                "scanned": scanned,
                "truncated": truncated,
                "groups": groups,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_waf_review_group import EdgeWAFReviewGroup

        d = dict(src_dict)
        since = datetime.datetime.fromisoformat(d.pop("since"))

        scanned = d.pop("scanned")

        truncated = d.pop("truncated")

        groups = []
        _groups = d.pop("groups")
        for groups_item_data in _groups:
            groups_item = EdgeWAFReviewGroup.from_dict(groups_item_data)

            groups.append(groups_item)

        edge_waf_review_response = cls(
            since=since,
            scanned=scanned,
            truncated=truncated,
            groups=groups,
        )

        edge_waf_review_response.additional_properties = d
        return edge_waf_review_response

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
