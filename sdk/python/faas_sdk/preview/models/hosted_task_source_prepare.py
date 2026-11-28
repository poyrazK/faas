from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.hosted_task_source_prepare_sources import HostedTaskSourcePrepareSources
    from ..models.hosted_task_source_review import HostedTaskSourceReview


T = TypeVar("T", bound="HostedTaskSourcePrepare")


@_attrs_define
class HostedTaskSourcePrepare:
    """Exact reviewed Node Task bundles submitted for encrypted retention under an immutable caller-chosen identity."""

    review: HostedTaskSourceReview
    """Canonical hosted source review, including source digests and native app baselines."""
    sources: HostedTaskSourcePrepareSources

    def to_dict(self) -> dict[str, Any]:
        review = self.review.to_dict()

        sources = self.sources.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "review": review,
                "sources": sources,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.hosted_task_source_prepare_sources import HostedTaskSourcePrepareSources
        from ..models.hosted_task_source_review import HostedTaskSourceReview

        d = dict(src_dict)
        review = HostedTaskSourceReview.from_dict(d.pop("review"))

        sources = HostedTaskSourcePrepareSources.from_dict(d.pop("sources"))

        hosted_task_source_prepare = cls(
            review=review,
            sources=sources,
        )

        return hosted_task_source_prepare
