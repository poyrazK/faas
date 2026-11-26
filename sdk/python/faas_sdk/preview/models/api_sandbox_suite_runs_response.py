from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_sandbox_suite_runs_response_automatic_status import (
    APISandboxSuiteRunsResponseAutomaticStatus,
    check_api_sandbox_suite_runs_response_automatic_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_sandbox_release_gate import APISandboxReleaseGate
    from ..models.api_sandbox_suite_run_summary import APISandboxSuiteRunSummary


T = TypeVar("T", bound="APISandboxSuiteRunsResponse")


@_attrs_define
class APISandboxSuiteRunsResponse:
    runs: list[APISandboxSuiteRunSummary]
    automatic_status: APISandboxSuiteRunsResponseAutomaticStatus | Unset = UNSET
    """Latest automatic launch status for this candidate when the app has requested an automatic run."""
    release_gate: APISandboxReleaseGate | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        runs = []
        for runs_item_data in self.runs:
            runs_item = runs_item_data.to_dict()
            runs.append(runs_item)

        automatic_status: str | Unset = UNSET
        if not isinstance(self.automatic_status, Unset):
            automatic_status = self.automatic_status

        release_gate: dict[str, Any] | Unset = UNSET
        if not isinstance(self.release_gate, Unset):
            release_gate = self.release_gate.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "runs": runs,
            }
        )
        if automatic_status is not UNSET:
            field_dict["automatic_status"] = automatic_status
        if release_gate is not UNSET:
            field_dict["release_gate"] = release_gate

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_release_gate import APISandboxReleaseGate
        from ..models.api_sandbox_suite_run_summary import APISandboxSuiteRunSummary

        d = dict(src_dict)
        runs = []
        _runs = d.pop("runs")
        for runs_item_data in _runs:
            runs_item = APISandboxSuiteRunSummary.from_dict(runs_item_data)

            runs.append(runs_item)

        _automatic_status = d.pop("automatic_status", UNSET)
        automatic_status: APISandboxSuiteRunsResponseAutomaticStatus | Unset
        if isinstance(_automatic_status, Unset):
            automatic_status = UNSET
        else:
            automatic_status = check_api_sandbox_suite_runs_response_automatic_status(_automatic_status)

        _release_gate = d.pop("release_gate", UNSET)
        release_gate: APISandboxReleaseGate | Unset
        if isinstance(_release_gate, Unset):
            release_gate = UNSET
        else:
            release_gate = APISandboxReleaseGate.from_dict(_release_gate)

        api_sandbox_suite_runs_response = cls(
            runs=runs,
            automatic_status=automatic_status,
            release_gate=release_gate,
        )

        api_sandbox_suite_runs_response.additional_properties = d
        return api_sandbox_suite_runs_response

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
