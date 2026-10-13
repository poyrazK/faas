from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_policy_review import WorkflowRecoveryPolicyReview


T = TypeVar("T", bound="WorkflowRecoveryPolicyReviews")


@_attrs_define
class WorkflowRecoveryPolicyReviews:
    """One identifier-ordered page of retained recovery policy reviews."""

    reviews: list[WorkflowRecoveryPolicyReview]
    next_after: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        reviews = []
        for reviews_item_data in self.reviews:
            reviews_item = reviews_item_data.to_dict()
            reviews.append(reviews_item)

        next_after: str | Unset = UNSET
        if not isinstance(self.next_after, Unset):
            next_after = str(self.next_after)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "reviews": reviews,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_policy_review import WorkflowRecoveryPolicyReview

        d = dict(src_dict)
        reviews = []
        _reviews = d.pop("reviews")
        for reviews_item_data in _reviews:
            reviews_item = WorkflowRecoveryPolicyReview.from_dict(reviews_item_data)

            reviews.append(reviews_item)

        _next_after = d.pop("next_after", UNSET)
        next_after: UUID | Unset
        if isinstance(_next_after, Unset):
            next_after = UNSET
        else:
            next_after = UUID(_next_after)

        workflow_recovery_policy_reviews = cls(
            reviews=reviews,
            next_after=next_after,
        )

        workflow_recovery_policy_reviews.additional_properties = d
        return workflow_recovery_policy_reviews

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
