from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_outbound_ai_prompt_evaluation_request_cases_item import (
        CreateOutboundAIPromptEvaluationRequestCasesItem,
    )
    from ..models.put_outbound_ai_prompt_canary_request import PutOutboundAIPromptCanaryRequest


T = TypeVar("T", bound="CreateOutboundAIPromptEvaluationRequest")


@_attrs_define
class CreateOutboundAIPromptEvaluationRequest:
    """A candidate version, the variable sets to run it on, and the judge model."""

    version: int
    cases: list[CreateOutboundAIPromptEvaluationRequestCasesItem]
    """Variable sets; each is rendered with the candidate and the live version."""
    judge_integration_id: UUID
    judge_model: str
    max_loss_rate: int | Unset = UNSET
    canary: PutOutboundAIPromptCanaryRequest | Unset = UNSET
    """Start or replace a prompt canary. Omitted margins take the route canary defaults."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        cases = []
        for cases_item_data in self.cases:
            cases_item = cases_item_data.to_dict()
            cases.append(cases_item)

        judge_integration_id = str(self.judge_integration_id)

        judge_model = self.judge_model

        max_loss_rate = self.max_loss_rate

        canary: dict[str, Any] | Unset = UNSET
        if not isinstance(self.canary, Unset):
            canary = self.canary.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "cases": cases,
                "judge_integration_id": judge_integration_id,
                "judge_model": judge_model,
            }
        )
        if max_loss_rate is not UNSET:
            field_dict["max_loss_rate"] = max_loss_rate
        if canary is not UNSET:
            field_dict["canary"] = canary

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_outbound_ai_prompt_evaluation_request_cases_item import (
            CreateOutboundAIPromptEvaluationRequestCasesItem,
        )
        from ..models.put_outbound_ai_prompt_canary_request import PutOutboundAIPromptCanaryRequest

        d = dict(src_dict)
        version = d.pop("version")

        cases = []
        _cases = d.pop("cases")
        for cases_item_data in _cases:
            cases_item = CreateOutboundAIPromptEvaluationRequestCasesItem.from_dict(cases_item_data)

            cases.append(cases_item)

        judge_integration_id = UUID(d.pop("judge_integration_id"))

        judge_model = d.pop("judge_model")

        max_loss_rate = d.pop("max_loss_rate", UNSET)

        _canary = d.pop("canary", UNSET)
        canary: PutOutboundAIPromptCanaryRequest | Unset
        if isinstance(_canary, Unset):
            canary = UNSET
        else:
            canary = PutOutboundAIPromptCanaryRequest.from_dict(_canary)

        create_outbound_ai_prompt_evaluation_request = cls(
            version=version,
            cases=cases,
            judge_integration_id=judge_integration_id,
            judge_model=judge_model,
            max_loss_rate=max_loss_rate,
            canary=canary,
        )

        create_outbound_ai_prompt_evaluation_request.additional_properties = d
        return create_outbound_ai_prompt_evaluation_request

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
