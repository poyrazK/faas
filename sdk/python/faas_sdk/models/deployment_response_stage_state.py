from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="DeploymentResponseStageState")


@_attrs_define
class DeploymentResponseStageState:
    """Actual stage progress, including retry_requested_stage and retry_restart_reason when prerequisites must be rebuilt.
    Optional hosting_verification records started_at, deadline_at, attempts, last_error_code, retry_not_before and
    completed_at during unavailable candidate verification recovery (ADR-481, ADR-482). last_error_code distinguishes
    publication, gateway, transport and candidate-evidence failures; a transport failure does not attribute blame to the
    app or platform. retry_not_before is an eligibility floor, not a promised delivery time; completed_at means the
    attempt finished, while the hosting receipt records its verdict.

    """

    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_response_stage_state = cls()

        deployment_response_stage_state.additional_properties = d
        return deployment_response_stage_state

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
