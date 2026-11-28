from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_prompt_evaluation_state import (
    OutboundAIPromptEvaluationState,
    check_outbound_ai_prompt_evaluation_state,
)
from ..models.outbound_ai_prompt_evaluation_verdict_type_1 import (
    OutboundAIPromptEvaluationVerdictType1,
    check_outbound_ai_prompt_evaluation_verdict_type_1,
)
from ..models.outbound_ai_prompt_evaluation_verdict_type_2_type_1 import (
    OutboundAIPromptEvaluationVerdictType2Type1,
    check_outbound_ai_prompt_evaluation_verdict_type_2_type_1,
)
from ..models.outbound_ai_prompt_evaluation_verdict_type_3_type_1 import (
    OutboundAIPromptEvaluationVerdictType3Type1,
    check_outbound_ai_prompt_evaluation_verdict_type_3_type_1,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.outbound_ai_evaluation_target_stats import OutboundAIEvaluationTargetStats
    from ..models.outbound_ai_prompt_evaluation_item import OutboundAIPromptEvaluationItem
    from ..models.put_outbound_ai_prompt_canary_request import PutOutboundAIPromptCanaryRequest


T = TypeVar("T", bound="OutboundAIPromptEvaluation")


@_attrs_define
class OutboundAIPromptEvaluation:
    """A comparison of a candidate prompt version against the live version."""

    id: UUID
    prompt: str
    baseline_version: int
    candidate_version: int
    judge_integration_id: UUID
    judge_model: str
    max_loss_rate: int
    canary: None | PutOutboundAIPromptCanaryRequest
    state: OutboundAIPromptEvaluationState
    verdict: (
        None
        | OutboundAIPromptEvaluationVerdictType1
        | OutboundAIPromptEvaluationVerdictType2Type1
        | OutboundAIPromptEvaluationVerdictType3Type1
    )
    error_code: None | str
    cases: int
    wins: int
    ties: int
    losses: int
    candidate_errors: int
    judge_errors: int
    baseline: OutboundAIEvaluationTargetStats
    """One side's total cost over the sample and mean time to first byte over the calls that reported one."""
    candidate: OutboundAIEvaluationTargetStats
    """One side's total cost over the sample and mean time to first byte over the calls that reported one."""
    canary_started: bool
    created_at: datetime.datetime
    started_at: datetime.datetime | None
    finished_at: datetime.datetime | None
    items: list[OutboundAIPromptEvaluationItem] | Unset = UNSET
    content_withheld: bool | Unset = UNSET
    """The items' answer texts were left out; reading them needs admin or secrets:read."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.put_outbound_ai_prompt_canary_request import PutOutboundAIPromptCanaryRequest

        id = str(self.id)

        prompt = self.prompt

        baseline_version = self.baseline_version

        candidate_version = self.candidate_version

        judge_integration_id = str(self.judge_integration_id)

        judge_model = self.judge_model

        max_loss_rate = self.max_loss_rate

        canary: dict[str, Any] | None
        if isinstance(self.canary, PutOutboundAIPromptCanaryRequest):
            canary = self.canary.to_dict()
        else:
            canary = self.canary

        state: str = self.state

        verdict: None | str
        if isinstance(self.verdict, str):
            verdict = self.verdict
        elif isinstance(self.verdict, str):
            verdict = self.verdict
        elif isinstance(self.verdict, str):
            verdict = self.verdict
        else:
            verdict = self.verdict

        error_code: None | str
        error_code = self.error_code

        cases = self.cases

        wins = self.wins

        ties = self.ties

        losses = self.losses

        candidate_errors = self.candidate_errors

        judge_errors = self.judge_errors

        baseline = self.baseline.to_dict()

        candidate = self.candidate.to_dict()

        canary_started = self.canary_started

        created_at = self.created_at.isoformat()

        started_at: None | str
        if isinstance(self.started_at, datetime.datetime):
            started_at = self.started_at.isoformat()
        else:
            started_at = self.started_at

        finished_at: None | str
        if isinstance(self.finished_at, datetime.datetime):
            finished_at = self.finished_at.isoformat()
        else:
            finished_at = self.finished_at

        items: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.items, Unset):
            items = []
            for items_item_data in self.items:
                items_item = items_item_data.to_dict()
                items.append(items_item)

        content_withheld = self.content_withheld

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "prompt": prompt,
                "baseline_version": baseline_version,
                "candidate_version": candidate_version,
                "judge_integration_id": judge_integration_id,
                "judge_model": judge_model,
                "max_loss_rate": max_loss_rate,
                "canary": canary,
                "state": state,
                "verdict": verdict,
                "error_code": error_code,
                "cases": cases,
                "wins": wins,
                "ties": ties,
                "losses": losses,
                "candidate_errors": candidate_errors,
                "judge_errors": judge_errors,
                "baseline": baseline,
                "candidate": candidate,
                "canary_started": canary_started,
                "created_at": created_at,
                "started_at": started_at,
                "finished_at": finished_at,
            }
        )
        if items is not UNSET:
            field_dict["items"] = items
        if content_withheld is not UNSET:
            field_dict["content_withheld"] = content_withheld

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_evaluation_target_stats import OutboundAIEvaluationTargetStats
        from ..models.outbound_ai_prompt_evaluation_item import OutboundAIPromptEvaluationItem
        from ..models.put_outbound_ai_prompt_canary_request import PutOutboundAIPromptCanaryRequest

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        prompt = d.pop("prompt")

        baseline_version = d.pop("baseline_version")

        candidate_version = d.pop("candidate_version")

        judge_integration_id = UUID(d.pop("judge_integration_id"))

        judge_model = d.pop("judge_model")

        max_loss_rate = d.pop("max_loss_rate")

        def _parse_canary(data: object) -> None | PutOutboundAIPromptCanaryRequest:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                canary_type_0 = PutOutboundAIPromptCanaryRequest.from_dict(data)

                return canary_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | PutOutboundAIPromptCanaryRequest, data)

        canary = _parse_canary(d.pop("canary"))

        state = check_outbound_ai_prompt_evaluation_state(d.pop("state"))

        def _parse_verdict(
            data: object,
        ) -> (
            None
            | OutboundAIPromptEvaluationVerdictType1
            | OutboundAIPromptEvaluationVerdictType2Type1
            | OutboundAIPromptEvaluationVerdictType3Type1
        ):
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                verdict_type_1 = check_outbound_ai_prompt_evaluation_verdict_type_1(data)

                return verdict_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                verdict_type_2_type_1 = check_outbound_ai_prompt_evaluation_verdict_type_2_type_1(data)

                return verdict_type_2_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                verdict_type_3_type_1 = check_outbound_ai_prompt_evaluation_verdict_type_3_type_1(data)

                return verdict_type_3_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(
                None
                | OutboundAIPromptEvaluationVerdictType1
                | OutboundAIPromptEvaluationVerdictType2Type1
                | OutboundAIPromptEvaluationVerdictType3Type1,
                data,
            )

        verdict = _parse_verdict(d.pop("verdict"))

        def _parse_error_code(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        error_code = _parse_error_code(d.pop("error_code"))

        cases = d.pop("cases")

        wins = d.pop("wins")

        ties = d.pop("ties")

        losses = d.pop("losses")

        candidate_errors = d.pop("candidate_errors")

        judge_errors = d.pop("judge_errors")

        baseline = OutboundAIEvaluationTargetStats.from_dict(d.pop("baseline"))

        candidate = OutboundAIEvaluationTargetStats.from_dict(d.pop("candidate"))

        canary_started = d.pop("canary_started")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        def _parse_started_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                started_at_type_0 = datetime.datetime.fromisoformat(data)

                return started_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        started_at = _parse_started_at(d.pop("started_at"))

        def _parse_finished_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                finished_at_type_0 = datetime.datetime.fromisoformat(data)

                return finished_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        finished_at = _parse_finished_at(d.pop("finished_at"))

        _items = d.pop("items", UNSET)
        items: list[OutboundAIPromptEvaluationItem] | Unset = UNSET
        if _items is not UNSET:
            items = []
            for items_item_data in _items:
                items_item = OutboundAIPromptEvaluationItem.from_dict(items_item_data)

                items.append(items_item)

        content_withheld = d.pop("content_withheld", UNSET)

        outbound_ai_prompt_evaluation = cls(
            id=id,
            prompt=prompt,
            baseline_version=baseline_version,
            candidate_version=candidate_version,
            judge_integration_id=judge_integration_id,
            judge_model=judge_model,
            max_loss_rate=max_loss_rate,
            canary=canary,
            state=state,
            verdict=verdict,
            error_code=error_code,
            cases=cases,
            wins=wins,
            ties=ties,
            losses=losses,
            candidate_errors=candidate_errors,
            judge_errors=judge_errors,
            baseline=baseline,
            candidate=candidate,
            canary_started=canary_started,
            created_at=created_at,
            started_at=started_at,
            finished_at=finished_at,
            items=items,
            content_withheld=content_withheld,
        )

        outbound_ai_prompt_evaluation.additional_properties = d
        return outbound_ai_prompt_evaluation

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
