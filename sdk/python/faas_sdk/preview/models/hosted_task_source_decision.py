from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.hosted_task_source_decision_decision import (
    HostedTaskSourceDecisionDecision,
    check_hosted_task_source_decision_decision,
)

T = TypeVar("T", bound="HostedTaskSourceDecision")


@_attrs_define
class HostedTaskSourceDecision:
    """Personal-owner source review decision bound to both immutable hosted source digests."""

    intent_sha256: str
    """Complete hosted source intent digest explicitly reviewed by the personal owner."""
    review_sha256: str
    """Exact retained hosted source-review fingerprint included in this owner decision."""
    decision: HostedTaskSourceDecisionDecision
    """Owner verdict on this hosted source review, independent of future execution authorization."""

    def to_dict(self) -> dict[str, Any]:
        intent_sha256 = self.intent_sha256

        review_sha256 = self.review_sha256

        decision: str = self.decision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "intent_sha256": intent_sha256,
                "review_sha256": review_sha256,
                "decision": decision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        intent_sha256 = d.pop("intent_sha256")

        review_sha256 = d.pop("review_sha256")

        decision = check_hosted_task_source_decision_decision(d.pop("decision"))

        hosted_task_source_decision = cls(
            intent_sha256=intent_sha256,
            review_sha256=review_sha256,
            decision=decision,
        )

        return hosted_task_source_decision
