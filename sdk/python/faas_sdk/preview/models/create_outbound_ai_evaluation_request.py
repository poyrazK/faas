from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.put_outbound_ai_route_canary_request import PutOutboundAIRouteCanaryRequest


T = TypeVar("T", bound="CreateOutboundAIEvaluationRequest")


@_attrs_define
class CreateOutboundAIEvaluationRequest:
    """Evaluate a route target against the route's first target. Omitted settings take their defaults: a sample of 20 and a
    max_loss_rate of 20. canary, when set, must name the same target and starts if the evaluation passes.

    """

    target: int
    """The 1-based route position evaluated."""
    judge_integration_id: UUID
    """One of the account's enabled AI integrations."""
    judge_model: str
    sample_size: int | Unset = UNSET
    max_loss_rate: int | Unset = UNSET
    canary: PutOutboundAIRouteCanaryRequest | Unset = UNSET
    """Start or replace a route's model canary. Omitted margins take their defaults: 5 error-rate points, 50 calls,
    30 minutes, and no latency margin."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        target = self.target

        judge_integration_id = str(self.judge_integration_id)

        judge_model = self.judge_model

        sample_size = self.sample_size

        max_loss_rate = self.max_loss_rate

        canary: dict[str, Any] | Unset = UNSET
        if not isinstance(self.canary, Unset):
            canary = self.canary.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "target": target,
                "judge_integration_id": judge_integration_id,
                "judge_model": judge_model,
            }
        )
        if sample_size is not UNSET:
            field_dict["sample_size"] = sample_size
        if max_loss_rate is not UNSET:
            field_dict["max_loss_rate"] = max_loss_rate
        if canary is not UNSET:
            field_dict["canary"] = canary

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.put_outbound_ai_route_canary_request import PutOutboundAIRouteCanaryRequest

        d = dict(src_dict)
        target = d.pop("target")

        judge_integration_id = UUID(d.pop("judge_integration_id"))

        judge_model = d.pop("judge_model")

        sample_size = d.pop("sample_size", UNSET)

        max_loss_rate = d.pop("max_loss_rate", UNSET)

        _canary = d.pop("canary", UNSET)
        canary: PutOutboundAIRouteCanaryRequest | Unset
        if isinstance(_canary, Unset):
            canary = UNSET
        else:
            canary = PutOutboundAIRouteCanaryRequest.from_dict(_canary)

        create_outbound_ai_evaluation_request = cls(
            target=target,
            judge_integration_id=judge_integration_id,
            judge_model=judge_model,
            sample_size=sample_size,
            max_loss_rate=max_loss_rate,
            canary=canary,
        )

        create_outbound_ai_evaluation_request.additional_properties = d
        return create_outbound_ai_evaluation_request

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
