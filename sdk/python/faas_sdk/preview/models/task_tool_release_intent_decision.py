from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.task_tool_release_intent_decision_decision import (
    TaskToolReleaseIntentDecisionDecision,
    check_task_tool_release_intent_decision_decision,
)

T = TypeVar("T", bound="TaskToolReleaseIntentDecision")


@_attrs_define
class TaskToolReleaseIntentDecision:
    """Owner verdict bound to an immutable Task intent digest and its retained source fingerprint."""

    intent_sha256: str
    release_fingerprint: str
    decision: TaskToolReleaseIntentDecisionDecision

    def to_dict(self) -> dict[str, Any]:
        intent_sha256 = self.intent_sha256

        release_fingerprint = self.release_fingerprint

        decision: str = self.decision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "intent_sha256": intent_sha256,
                "release_fingerprint": release_fingerprint,
                "decision": decision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        intent_sha256 = d.pop("intent_sha256")

        release_fingerprint = d.pop("release_fingerprint")

        decision = check_task_tool_release_intent_decision_decision(d.pop("decision"))

        task_tool_release_intent_decision = cls(
            intent_sha256=intent_sha256,
            release_fingerprint=release_fingerprint,
            decision=decision,
        )

        return task_tool_release_intent_decision
