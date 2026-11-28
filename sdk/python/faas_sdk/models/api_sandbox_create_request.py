from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="APISandboxCreateRequest")


@_attrs_define
class APISandboxCreateRequest:
    """Configuration for a fresh copy of an API release."""

    smoke_path: str | Unset = UNSET
    """Optional absolute path on the sandbox host to check with GET. Use the same value when retrying with this
    sandbox UUID."""
    scenario_revision: UUID | Unset = UNSET
    """Copy the selected app library or release scenario revision into the sandbox. Reuse this value when retrying
    the same sandbox UUID."""
    partner_label: str | Unset = UNSET
    """Optional recipient label. Requires scenario_revision and creates a private copy. Raw partner progress is
    available to the owner."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        smoke_path = self.smoke_path

        scenario_revision: str | Unset = UNSET
        if not isinstance(self.scenario_revision, Unset):
            scenario_revision = str(self.scenario_revision)

        partner_label = self.partner_label

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if smoke_path is not UNSET:
            field_dict["smoke_path"] = smoke_path
        if scenario_revision is not UNSET:
            field_dict["scenario_revision"] = scenario_revision
        if partner_label is not UNSET:
            field_dict["partner_label"] = partner_label

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        smoke_path = d.pop("smoke_path", UNSET)

        _scenario_revision = d.pop("scenario_revision", UNSET)
        scenario_revision: UUID | Unset
        if isinstance(_scenario_revision, Unset):
            scenario_revision = UNSET
        else:
            scenario_revision = UUID(_scenario_revision)

        partner_label = d.pop("partner_label", UNSET)

        api_sandbox_create_request = cls(
            smoke_path=smoke_path,
            scenario_revision=scenario_revision,
            partner_label=partner_label,
        )

        api_sandbox_create_request.additional_properties = d
        return api_sandbox_create_request

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
