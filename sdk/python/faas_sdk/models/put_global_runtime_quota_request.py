from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.global_runtime_quota_share import GlobalRuntimeQuotaShare


T = TypeVar("T", bound="PutGlobalRuntimeQuotaRequest")


@_attrs_define
class PutGlobalRuntimeQuotaRequest:
    """Immutable regional share allocation and optional placement of rollout headroom."""

    shares: list[GlobalRuntimeQuotaShare]
    """One positive share for every selected verified region; duplicate regions are rejected."""
    rollout_region: str | Unset = UNSET
    """Selected region receiving rollout overlap; omitted or empty defaults to the first sorted region."""

    def to_dict(self) -> dict[str, Any]:
        shares = []
        for shares_item_data in self.shares:
            shares_item = shares_item_data.to_dict()
            shares.append(shares_item)

        rollout_region = self.rollout_region

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "shares": shares,
            }
        )
        if rollout_region is not UNSET:
            field_dict["rollout_region"] = rollout_region

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.global_runtime_quota_share import GlobalRuntimeQuotaShare

        d = dict(src_dict)
        shares = []
        _shares = d.pop("shares")
        for shares_item_data in _shares:
            shares_item = GlobalRuntimeQuotaShare.from_dict(shares_item_data)

            shares.append(shares_item)

        rollout_region = d.pop("rollout_region", UNSET)

        put_global_runtime_quota_request = cls(
            shares=shares,
            rollout_region=rollout_region,
        )

        return put_global_runtime_quota_request
