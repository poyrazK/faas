from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="SetAccountEgressAllowlistExtraRequest")


@_attrs_define
class SetAccountEgressAllowlistExtraRequest:
    """Body of PATCH /v1/account/egress_allowlist_extra (issue #679 / PR-B / ADR-082). `extra` is the per-account additive
    budget on top of the plan's `apps.egress_allowlist` cap. `extra=0` clears the override (the plan cap is
    authoritative again); negative values or values above `max_extra` (1024) are rejected with
    `account_egress_allowlist_extra_out_of_range`.

    """

    extra: int
    """Requested additive budget, in CIDR count. 0 = clear the override; values above max_extra are rejected."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        extra = self.extra

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "extra": extra,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        extra = d.pop("extra")

        set_account_egress_allowlist_extra_request = cls(
            extra=extra,
        )

        set_account_egress_allowlist_extra_request.additional_properties = d
        return set_account_egress_allowlist_extra_request

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
