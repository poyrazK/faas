from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_initial_recovery_review import WorkflowInitialRecoveryReview


T = TypeVar("T", bound="WorkflowInitialRecoveryReviews")


@_attrs_define
class WorkflowInitialRecoveryReviews:
    """Page of durable initial recovery reviews."""

    reviews: list[WorkflowInitialRecoveryReview]
    next_after: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        reviews = []
        for reviews_item_data in self.reviews:
            reviews_item = reviews_item_data.to_dict()
            reviews.append(reviews_item)

        next_after: str | Unset = UNSET
        if not isinstance(self.next_after, Unset):
            next_after = str(self.next_after)

        field_dict: dict[str, Any] = {}

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
        from ..models.workflow_initial_recovery_review import WorkflowInitialRecoveryReview

        d = dict(src_dict)
        reviews = []
        _reviews = d.pop("reviews")
        for reviews_item_data in _reviews:
            reviews_item = WorkflowInitialRecoveryReview.from_dict(reviews_item_data)

            reviews.append(reviews_item)

        _next_after = d.pop("next_after", UNSET)
        next_after: UUID | Unset
        if isinstance(_next_after, Unset):
            next_after = UNSET
        else:
            next_after = UUID(_next_after)

        workflow_initial_recovery_reviews = cls(
            reviews=reviews,
            next_after=next_after,
        )

        return workflow_initial_recovery_reviews
