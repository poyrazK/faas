from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OutboundAIOnlineEvaluation")


@_attrs_define
class OutboundAIOnlineEvaluation:
    """Score sample_percent of answered app calls from 1 to 5 with judge_model through judge_integration_id, one of the
    account's AI integrations, metered under the caller evaluation (ADR 20261021171712).

    """

    sample_percent: int
    judge_integration_id: UUID
    judge_model: str
    rubric: str | Unset = UNSET
    """What a good answer does for this app"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sample_percent = self.sample_percent

        judge_integration_id = str(self.judge_integration_id)

        judge_model = self.judge_model

        rubric = self.rubric

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sample_percent": sample_percent,
                "judge_integration_id": judge_integration_id,
                "judge_model": judge_model,
            }
        )
        if rubric is not UNSET:
            field_dict["rubric"] = rubric

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sample_percent = d.pop("sample_percent")

        judge_integration_id = UUID(d.pop("judge_integration_id"))

        judge_model = d.pop("judge_model")

        rubric = d.pop("rubric", UNSET)

        outbound_ai_online_evaluation = cls(
            sample_percent=sample_percent,
            judge_integration_id=judge_integration_id,
            judge_model=judge_model,
            rubric=rubric,
        )

        outbound_ai_online_evaluation.additional_properties = d
        return outbound_ai_online_evaluation

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
