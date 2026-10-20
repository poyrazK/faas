from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.commit_routing_version import CommitRoutingVersion, check_commit_routing_version
from ..types import UNSET, Unset

T = TypeVar("T", bound="CommitRouting")


@_attrs_define
class CommitRouting:
    """Version 2 routing authority is granted by the account owner on the source. Customer identity is separate from
    untrusted event data. Business keys share a queue only within the same policy and customer scope; scalar types
    remain distinct. Canonical keys are limited to 256 bytes including the type prefix.

    """

    version: CommitRoutingVersion
    key: bool | float | str
    platform_tenant_id: UUID | Unset = UNSET
    """Required for sources with allow_tenant_selection; forbidden otherwise. Must be active, in the source account
    and linked to the target app through an active surface."""

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        key: bool | float | str
        key = self.key

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "key": key,
            }
        )
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = check_commit_routing_version(d.pop("version"))

        def _parse_key(data: object) -> bool | float | str:
            return cast(bool | float | str, data)

        key = _parse_key(d.pop("key"))

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        commit_routing = cls(
            version=version,
            key=key,
            platform_tenant_id=platform_tenant_id,
        )

        return commit_routing
