from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventRoutingRecoveryExpectation")


@_attrs_define
class EventRoutingRecoveryExpectation:
    """Reviewed acceptance-time automation routing state. All comparisons occur in the replay transaction; mismatches
    return 409 event_recovery_changed without queuing a replay. Generation must be supplied for recipient routing and
    omitted for legacy routing. Does not bypass admission rules.

    """

    workflow_name: str
    published_version: int
    replay_count: int
    generation: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        workflow_name = self.workflow_name

        published_version = self.published_version

        replay_count = self.replay_count

        generation = self.generation

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow_name": workflow_name,
                "published_version": published_version,
                "replay_count": replay_count,
            }
        )
        if generation is not UNSET:
            field_dict["generation"] = generation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workflow_name = d.pop("workflow_name")

        published_version = d.pop("published_version")

        replay_count = d.pop("replay_count")

        generation = d.pop("generation", UNSET)

        event_routing_recovery_expectation = cls(
            workflow_name=workflow_name,
            published_version=published_version,
            replay_count=replay_count,
            generation=generation,
        )

        return event_routing_recovery_expectation
