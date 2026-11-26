from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_sandbox_run_request_mode import APISandboxRunRequestMode, check_api_sandbox_run_request_mode
from ..types import UNSET, Unset

T = TypeVar("T", bound="APISandboxRunRequest")


@_attrs_define
class APISandboxRunRequest:
    scenario_revision: UUID
    mode: APISandboxRunRequestMode | Unset = "automated"
    partner_webhook: bool | Unset = UNSET
    """Use the receiver registered for this outbound run."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scenario_revision = str(self.scenario_revision)

        mode: str | Unset = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode

        partner_webhook = self.partner_webhook

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scenario_revision": scenario_revision,
            }
        )
        if mode is not UNSET:
            field_dict["mode"] = mode
        if partner_webhook is not UNSET:
            field_dict["partner_webhook"] = partner_webhook

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        scenario_revision = UUID(d.pop("scenario_revision"))

        _mode = d.pop("mode", UNSET)
        mode: APISandboxRunRequestMode | Unset
        if isinstance(_mode, Unset):
            mode = UNSET
        else:
            mode = check_api_sandbox_run_request_mode(_mode)

        partner_webhook = d.pop("partner_webhook", UNSET)

        api_sandbox_run_request = cls(
            scenario_revision=scenario_revision,
            mode=mode,
            partner_webhook=partner_webhook,
        )

        api_sandbox_run_request.additional_properties = d
        return api_sandbox_run_request

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
