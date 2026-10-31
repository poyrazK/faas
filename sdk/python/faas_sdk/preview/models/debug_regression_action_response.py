from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_regression_item import DebugRegressionItem
    from ..models.debug_regression_review import DebugRegressionReview


T = TypeVar("T", bound="DebugRegressionActionResponse")


@_attrs_define
class DebugRegressionActionResponse:
    """Updated regression plus the exact workflow receipt. Older servers may omit review."""

    regression: DebugRegressionItem
    """One regression observation row."""
    review: DebugRegressionReview | Unset = UNSET
    """Exact workflow-only observation. Opaque version invalidates on every update; state is stored triage
    metadata, not health or effective inbox visibility."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        regression = self.regression.to_dict()

        review: dict[str, Any] | Unset = UNSET
        if not isinstance(self.review, Unset):
            review = self.review.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "regression": regression,
            }
        )
        if review is not UNSET:
            field_dict["review"] = review

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_regression_item import DebugRegressionItem
        from ..models.debug_regression_review import DebugRegressionReview

        d = dict(src_dict)
        regression = DebugRegressionItem.from_dict(d.pop("regression"))

        _review = d.pop("review", UNSET)
        review: DebugRegressionReview | Unset
        if isinstance(_review, Unset):
            review = UNSET
        else:
            review = DebugRegressionReview.from_dict(_review)

        debug_regression_action_response = cls(
            regression=regression,
            review=review,
        )

        debug_regression_action_response.additional_properties = d
        return debug_regression_action_response

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
