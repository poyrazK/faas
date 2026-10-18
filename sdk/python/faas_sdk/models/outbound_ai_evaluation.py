from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_evaluation_state import OutboundAIEvaluationState, check_outbound_ai_evaluation_state
from ..models.outbound_ai_evaluation_verdict_type_0 import (
    OutboundAIEvaluationVerdictType0,
    check_outbound_ai_evaluation_verdict_type_0,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.outbound_ai_evaluation_item import OutboundAIEvaluationItem
    from ..models.outbound_ai_evaluation_target_stats import OutboundAIEvaluationTargetStats
    from ..models.put_outbound_ai_route_canary_request import PutOutboundAIRouteCanaryRequest


T = TypeVar("T", bound="OutboundAIEvaluation")


@_attrs_define
class OutboundAIEvaluation:
    """An evaluation of a route target (ADR-1431). verdict is set when state is done, error_code when it is failed. items
    are returned only when one evaluation is read.

    """

    id: UUID
    route_id: UUID
    target: int
    judge_integration_id: UUID
    judge_model: str
    sample_size: int
    max_loss_rate: int
    canary: None | PutOutboundAIRouteCanaryRequest
    state: OutboundAIEvaluationState
    verdict: None | OutboundAIEvaluationVerdictType0
    error_code: None | str
    samples: int
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
    items: list[OutboundAIEvaluationItem] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.put_outbound_ai_route_canary_request import PutOutboundAIRouteCanaryRequest

        id = str(self.id)

        route_id = str(self.route_id)

        target = self.target

        judge_integration_id = str(self.judge_integration_id)

        judge_model = self.judge_model

        sample_size = self.sample_size

        max_loss_rate = self.max_loss_rate

        canary: dict[str, Any] | None
        if isinstance(self.canary, PutOutboundAIRouteCanaryRequest):
            canary = self.canary.to_dict()
        else:
            canary = self.canary

        state: str = self.state

        verdict: None | str
        if isinstance(self.verdict, str):
            verdict = self.verdict
        else:
            verdict = self.verdict

        error_code: None | str
        error_code = self.error_code

        samples = self.samples

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

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "route_id": route_id,
                "target": target,
                "judge_integration_id": judge_integration_id,
                "judge_model": judge_model,
                "sample_size": sample_size,
                "max_loss_rate": max_loss_rate,
                "canary": canary,
                "state": state,
                "verdict": verdict,
                "error_code": error_code,
                "samples": samples,
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_evaluation_item import OutboundAIEvaluationItem
        from ..models.outbound_ai_evaluation_target_stats import OutboundAIEvaluationTargetStats
        from ..models.put_outbound_ai_route_canary_request import PutOutboundAIRouteCanaryRequest

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        route_id = UUID(d.pop("route_id"))

        target = d.pop("target")

        judge_integration_id = UUID(d.pop("judge_integration_id"))

        judge_model = d.pop("judge_model")

        sample_size = d.pop("sample_size")

        max_loss_rate = d.pop("max_loss_rate")

        def _parse_canary(data: object) -> None | PutOutboundAIRouteCanaryRequest:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                canary_type_0 = PutOutboundAIRouteCanaryRequest.from_dict(data)

                return canary_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | PutOutboundAIRouteCanaryRequest, data)

        canary = _parse_canary(d.pop("canary"))

        state = check_outbound_ai_evaluation_state(d.pop("state"))

        def _parse_verdict(data: object) -> None | OutboundAIEvaluationVerdictType0:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                verdict_type_0 = check_outbound_ai_evaluation_verdict_type_0(data)

                return verdict_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | OutboundAIEvaluationVerdictType0, data)

        verdict = _parse_verdict(d.pop("verdict"))

        def _parse_error_code(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        error_code = _parse_error_code(d.pop("error_code"))

        samples = d.pop("samples")

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
        items: list[OutboundAIEvaluationItem] | Unset = UNSET
        if _items is not UNSET:
            items = []
            for items_item_data in _items:
                items_item = OutboundAIEvaluationItem.from_dict(items_item_data)

                items.append(items_item)

        outbound_ai_evaluation = cls(
            id=id,
            route_id=route_id,
            target=target,
            judge_integration_id=judge_integration_id,
            judge_model=judge_model,
            sample_size=sample_size,
            max_loss_rate=max_loss_rate,
            canary=canary,
            state=state,
            verdict=verdict,
            error_code=error_code,
            samples=samples,
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
        )

        outbound_ai_evaluation.additional_properties = d
        return outbound_ai_evaluation

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
