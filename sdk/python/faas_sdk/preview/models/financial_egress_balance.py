from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="FinancialEgressBalance")


@_attrs_define
class FinancialEgressBalance:
    """Egress contribution to a shared account compute and egress budget. Its funding allowance is pinned to the lowest
    recorded grant for the UTC month; increases benefit funding in a new month. Funded interface bytes include framing
    margin. Unused or uncertain credits commit conservatively after physical absence; invoice usage remains measured
    bytes.

    """

    initialized: bool
    committed_bytes: int
    reserved_bytes: int
    allowance_granted_bytes: int
    allowance_committed_bytes: int
    allowance_reserved_bytes: int
    allowance_remaining_bytes: int
    committed_millicents: int
    reserved_millicents: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        initialized = self.initialized

        committed_bytes = self.committed_bytes

        reserved_bytes = self.reserved_bytes

        allowance_granted_bytes = self.allowance_granted_bytes

        allowance_committed_bytes = self.allowance_committed_bytes

        allowance_reserved_bytes = self.allowance_reserved_bytes

        allowance_remaining_bytes = self.allowance_remaining_bytes

        committed_millicents = self.committed_millicents

        reserved_millicents = self.reserved_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "initialized": initialized,
                "committed_bytes": committed_bytes,
                "reserved_bytes": reserved_bytes,
                "allowance_granted_bytes": allowance_granted_bytes,
                "allowance_committed_bytes": allowance_committed_bytes,
                "allowance_reserved_bytes": allowance_reserved_bytes,
                "allowance_remaining_bytes": allowance_remaining_bytes,
                "committed_millicents": committed_millicents,
                "reserved_millicents": reserved_millicents,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        initialized = d.pop("initialized")

        committed_bytes = d.pop("committed_bytes")

        reserved_bytes = d.pop("reserved_bytes")

        allowance_granted_bytes = d.pop("allowance_granted_bytes")

        allowance_committed_bytes = d.pop("allowance_committed_bytes")

        allowance_reserved_bytes = d.pop("allowance_reserved_bytes")

        allowance_remaining_bytes = d.pop("allowance_remaining_bytes")

        committed_millicents = d.pop("committed_millicents")

        reserved_millicents = d.pop("reserved_millicents")

        financial_egress_balance = cls(
            initialized=initialized,
            committed_bytes=committed_bytes,
            reserved_bytes=reserved_bytes,
            allowance_granted_bytes=allowance_granted_bytes,
            allowance_committed_bytes=allowance_committed_bytes,
            allowance_reserved_bytes=allowance_reserved_bytes,
            allowance_remaining_bytes=allowance_remaining_bytes,
            committed_millicents=committed_millicents,
            reserved_millicents=reserved_millicents,
        )

        financial_egress_balance.additional_properties = d
        return financial_egress_balance

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
