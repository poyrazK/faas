from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RotateOrgAPIKeyRequest")


@_attrs_define
class RotateOrgAPIKeyRequest:
    """POST /v1/orgs/{slug}/keys/{id}/rotate body. `label` overrides the new key's label (inherits from the predecessor
    when omitted); `grace_window_days` is the same per-account override as `PATCH /v1/account/keys/grace_window_days` —
    defaulting to the plan default when omitted (`api.DefaultAPIKeyGraceWindowDays = 7`).

    """

    label: str | Unset = UNSET
    grace_window_days: int | Unset = UNSET
    """Days the predecessor stays valid after rotation. `0` is atomic; omitted/null falls back to the plan default."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        grace_window_days = self.grace_window_days

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if label is not UNSET:
            field_dict["label"] = label
        if grace_window_days is not UNSET:
            field_dict["grace_window_days"] = grace_window_days

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        label = d.pop("label", UNSET)

        grace_window_days = d.pop("grace_window_days", UNSET)

        rotate_org_api_key_request = cls(
            label=label,
            grace_window_days=grace_window_days,
        )

        rotate_org_api_key_request.additional_properties = d
        return rotate_org_api_key_request

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
