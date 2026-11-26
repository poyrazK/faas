from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_sandbox_release_gate_status import APISandboxReleaseGateStatus, check_api_sandbox_release_gate_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="APISandboxReleaseGate")


@_attrs_define
class APISandboxReleaseGate:
    """Current suite requirement and the run evaluated at release admission."""

    status: APISandboxReleaseGateStatus
    """Current requirement status or the recorded decision after command admission."""
    suite_run_id: UUID | Unset = UNSET
    """The evaluated suite run or the run accepted at admission."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        suite_run_id: str | Unset = UNSET
        if not isinstance(self.suite_run_id, Unset):
            suite_run_id = str(self.suite_run_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
            }
        )
        if suite_run_id is not UNSET:
            field_dict["suite_run_id"] = suite_run_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_api_sandbox_release_gate_status(d.pop("status"))

        _suite_run_id = d.pop("suite_run_id", UNSET)
        suite_run_id: UUID | Unset
        if isinstance(_suite_run_id, Unset):
            suite_run_id = UNSET
        else:
            suite_run_id = UUID(_suite_run_id)

        api_sandbox_release_gate = cls(
            status=status,
            suite_run_id=suite_run_id,
        )

        api_sandbox_release_gate.additional_properties = d
        return api_sandbox_release_gate

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
