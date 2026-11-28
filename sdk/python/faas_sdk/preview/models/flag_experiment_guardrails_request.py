from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.flag_experiment_guardrail_request import FlagExperimentGuardrailRequest


T = TypeVar("T", bound="FlagExperimentGuardrailsRequest")


@_attrs_define
class FlagExperimentGuardrailsRequest:
    """Changes the default guardrails; both are on unless turned off."""

    server_error_rate: FlagExperimentGuardrailRequest | Unset = UNSET
    """Turns one guardrail off or sets its tolerance."""
    p95_latency: FlagExperimentGuardrailRequest | Unset = UNSET
    """Turns one guardrail off or sets its tolerance."""

    def to_dict(self) -> dict[str, Any]:
        server_error_rate: dict[str, Any] | Unset = UNSET
        if not isinstance(self.server_error_rate, Unset):
            server_error_rate = self.server_error_rate.to_dict()

        p95_latency: dict[str, Any] | Unset = UNSET
        if not isinstance(self.p95_latency, Unset):
            p95_latency = self.p95_latency.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if server_error_rate is not UNSET:
            field_dict["server_error_rate"] = server_error_rate
        if p95_latency is not UNSET:
            field_dict["p95_latency"] = p95_latency

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_experiment_guardrail_request import FlagExperimentGuardrailRequest

        d = dict(src_dict)
        _server_error_rate = d.pop("server_error_rate", UNSET)
        server_error_rate: FlagExperimentGuardrailRequest | Unset
        if isinstance(_server_error_rate, Unset):
            server_error_rate = UNSET
        else:
            server_error_rate = FlagExperimentGuardrailRequest.from_dict(_server_error_rate)

        _p95_latency = d.pop("p95_latency", UNSET)
        p95_latency: FlagExperimentGuardrailRequest | Unset
        if isinstance(_p95_latency, Unset):
            p95_latency = UNSET
        else:
            p95_latency = FlagExperimentGuardrailRequest.from_dict(_p95_latency)

        flag_experiment_guardrails_request = cls(
            server_error_rate=server_error_rate,
            p95_latency=p95_latency,
        )

        return flag_experiment_guardrails_request
