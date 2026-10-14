from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RolloutDecisionSignals")


@_attrs_define
class RolloutDecisionSignals:
    """The candidate's OOM kills and both sides' liveness-driven restarts over the window."""

    oom_kills: float
    candidate_liveness_restarts: float
    stable_liveness_restarts: float
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        oom_kills = self.oom_kills

        candidate_liveness_restarts = self.candidate_liveness_restarts

        stable_liveness_restarts = self.stable_liveness_restarts

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "oom_kills": oom_kills,
                "candidate_liveness_restarts": candidate_liveness_restarts,
                "stable_liveness_restarts": stable_liveness_restarts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        oom_kills = d.pop("oom_kills")

        candidate_liveness_restarts = d.pop("candidate_liveness_restarts")

        stable_liveness_restarts = d.pop("stable_liveness_restarts")

        rollout_decision_signals = cls(
            oom_kills=oom_kills,
            candidate_liveness_restarts=candidate_liveness_restarts,
            stable_liveness_restarts=stable_liveness_restarts,
        )

        rollout_decision_signals.additional_properties = d
        return rollout_decision_signals

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
