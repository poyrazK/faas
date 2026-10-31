from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ExclusiveTriggerBindingRequest")


@_attrs_define
class ExclusiveTriggerBindingRequest:
    """Configuration that routes one trusted trigger through an exclusive-operation policy."""

    policy: str
    key: bool | float | str
    """Business coordination key; it never sets account or customer security scope."""
    platform_tenant_id: UUID | Unset = UNSET
    """Account-owner configured trusted tenant identity for tenant-scoped triggers."""
    equivalence_key: str | Unset = UNSET
    """Required by join_existing policies; used to distinguish equivalent requests."""

    def to_dict(self) -> dict[str, Any]:
        policy = self.policy

        key: bool | float | str
        key = self.key

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        equivalence_key = self.equivalence_key

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "policy": policy,
                "key": key,
            }
        )
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id
        if equivalence_key is not UNSET:
            field_dict["equivalence_key"] = equivalence_key

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        policy = d.pop("policy")

        def _parse_key(data: object) -> bool | float | str:
            return cast(bool | float | str, data)

        key = _parse_key(d.pop("key"))

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        equivalence_key = d.pop("equivalence_key", UNSET)

        exclusive_trigger_binding_request = cls(
            policy=policy,
            key=key,
            platform_tenant_id=platform_tenant_id,
            equivalence_key=equivalence_key,
        )

        return exclusive_trigger_binding_request
