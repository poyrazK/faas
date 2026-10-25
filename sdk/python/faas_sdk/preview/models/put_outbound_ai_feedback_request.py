from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.put_outbound_ai_feedback_request_rating import (
    PutOutboundAIFeedbackRequestRating,
    check_put_outbound_ai_feedback_request_rating,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PutOutboundAIFeedbackRequest")


@_attrs_define
class PutOutboundAIFeedbackRequest:
    """A rating of one logged call (ADR 20261021171712). A later rating replaces the earlier one."""

    rating: PutOutboundAIFeedbackRequestRating
    label: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rating: str = self.rating

        label = self.label

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rating": rating,
            }
        )
        if label is not UNSET:
            field_dict["label"] = label

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rating = check_put_outbound_ai_feedback_request_rating(d.pop("rating"))

        label = d.pop("label", UNSET)

        put_outbound_ai_feedback_request = cls(
            rating=rating,
            label=label,
        )

        put_outbound_ai_feedback_request.additional_properties = d
        return put_outbound_ai_feedback_request

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
