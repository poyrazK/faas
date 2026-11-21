from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_serving_probe_diagnostics_failure_code import (
    ObsServingProbeDiagnosticsFailureCode,
    check_obs_serving_probe_diagnostics_failure_code,
)
from ..models.obs_serving_probe_diagnostics_first_failure_code import (
    ObsServingProbeDiagnosticsFirstFailureCode,
    check_obs_serving_probe_diagnostics_first_failure_code,
)
from ..models.obs_serving_probe_diagnostics_reason import (
    ObsServingProbeDiagnosticsReason,
    check_obs_serving_probe_diagnostics_reason,
)
from ..models.obs_serving_probe_diagnostics_state import (
    ObsServingProbeDiagnosticsState,
    check_obs_serving_probe_diagnostics_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsServingProbeDiagnostics")


@_attrs_define
class ObsServingProbeDiagnostics:
    """Optional diagnostics from the same evaluated serving run. Available
    diagnostics have complete, coherent fixed codes and retry metadata.
    Unknown diagnostics have no numeric values or inferred success. This
    section does not change coverage state. Raw URLs, bodies and exceptions
    are excluded; none is success and unknown is an unclassified failure.

    """

    state: ObsServingProbeDiagnosticsState
    evaluated_at: datetime.datetime | None
    attempts: int | None
    http_status: int | None
    recovered_on_retry: bool | None
    reason: ObsServingProbeDiagnosticsReason | Unset = UNSET
    failure_code: ObsServingProbeDiagnosticsFailureCode | Unset = UNSET
    first_failure_code: ObsServingProbeDiagnosticsFirstFailureCode | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        evaluated_at: None | str
        if isinstance(self.evaluated_at, datetime.datetime):
            evaluated_at = self.evaluated_at.isoformat()
        else:
            evaluated_at = self.evaluated_at

        attempts: int | None
        attempts = self.attempts

        http_status: int | None
        http_status = self.http_status

        recovered_on_retry: bool | None
        recovered_on_retry = self.recovered_on_retry

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        failure_code: str | Unset = UNSET
        if not isinstance(self.failure_code, Unset):
            failure_code = self.failure_code

        first_failure_code: str | Unset = UNSET
        if not isinstance(self.first_failure_code, Unset):
            first_failure_code = self.first_failure_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "evaluated_at": evaluated_at,
                "attempts": attempts,
                "http_status": http_status,
                "recovered_on_retry": recovered_on_retry,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if first_failure_code is not UNSET:
            field_dict["first_failure_code"] = first_failure_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_obs_serving_probe_diagnostics_state(d.pop("state"))

        def _parse_evaluated_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                evaluated_at_type_0 = datetime.datetime.fromisoformat(data)

                return evaluated_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        evaluated_at = _parse_evaluated_at(d.pop("evaluated_at"))

        def _parse_attempts(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        attempts = _parse_attempts(d.pop("attempts"))

        def _parse_http_status(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        http_status = _parse_http_status(d.pop("http_status"))

        def _parse_recovered_on_retry(data: object) -> bool | None:
            if data is None:
                return data
            return cast(bool | None, data)

        recovered_on_retry = _parse_recovered_on_retry(d.pop("recovered_on_retry"))

        _reason = d.pop("reason", UNSET)
        reason: ObsServingProbeDiagnosticsReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_serving_probe_diagnostics_reason(_reason)

        _failure_code = d.pop("failure_code", UNSET)
        failure_code: ObsServingProbeDiagnosticsFailureCode | Unset
        if isinstance(_failure_code, Unset):
            failure_code = UNSET
        else:
            failure_code = check_obs_serving_probe_diagnostics_failure_code(_failure_code)

        _first_failure_code = d.pop("first_failure_code", UNSET)
        first_failure_code: ObsServingProbeDiagnosticsFirstFailureCode | Unset
        if isinstance(_first_failure_code, Unset):
            first_failure_code = UNSET
        else:
            first_failure_code = check_obs_serving_probe_diagnostics_first_failure_code(_first_failure_code)

        obs_serving_probe_diagnostics = cls(
            state=state,
            evaluated_at=evaluated_at,
            attempts=attempts,
            http_status=http_status,
            recovered_on_retry=recovered_on_retry,
            reason=reason,
            failure_code=failure_code,
            first_failure_code=first_failure_code,
        )

        obs_serving_probe_diagnostics.additional_properties = d
        return obs_serving_probe_diagnostics

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
