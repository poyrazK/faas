from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_collection_rejection_reason import (
    ObsAlertCollectionRejectionReason,
    check_obs_alert_collection_rejection_reason,
)
from ..models.obs_alert_collection_rejection_scope import (
    ObsAlertCollectionRejectionScope,
    check_obs_alert_collection_rejection_scope,
)

if TYPE_CHECKING:
    from ..models.obs_rule_health_item import ObsRuleHealthItem
    from ..models.obs_rule_health_totals import ObsRuleHealthTotals


T = TypeVar("T", bound="ObsAlertCollectionRejection")


@_attrs_define
class ObsAlertCollectionRejection:
    """Sanitized observations from the exact rejected source; complete counts and an unhealthy-first preview, bounded to 16
    KiB of compact JSON.

    """

    scope: ObsAlertCollectionRejectionScope
    reason: ObsAlertCollectionRejectionReason
    """Distinguishes observed rule issues, a known empty loaded inventory, and rejection without a matching rule-
    level diagnostic."""
    totals: ObsRuleHealthTotals
    """Complete bounded source counts. Rule health categories partition rules; invalid groups include empty groups
    with missing or duplicate identity."""
    items: list[ObsRuleHealthItem]
    """Unhealthy rules and invalid empty groups, ordered by issue priority then hashed group and item identity;
    ages refer to attempted_at."""
    total_items: int
    """Complete count of unhealthy diagnostic items before the persisted preview is bounded."""
    limit: Literal[10]
    truncated: bool
    """Whether the item or byte bound omitted diagnostic items included in complete totals."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope: str = self.scope

        reason: str = self.reason

        totals = self.totals.to_dict()

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        total_items = self.total_items

        limit = self.limit

        truncated = self.truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "reason": reason,
                "totals": totals,
                "items": items,
                "total_items": total_items,
                "limit": limit,
                "truncated": truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_rule_health_item import ObsRuleHealthItem
        from ..models.obs_rule_health_totals import ObsRuleHealthTotals

        d = dict(src_dict)
        scope = check_obs_alert_collection_rejection_scope(d.pop("scope"))

        reason = check_obs_alert_collection_rejection_reason(d.pop("reason"))

        totals = ObsRuleHealthTotals.from_dict(d.pop("totals"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuleHealthItem.from_dict(items_item_data)

            items.append(items_item)

        total_items = d.pop("total_items")

        limit = cast(Literal[10], d.pop("limit"))
        if limit != 10:
            raise ValueError(f"limit must match const 10, got '{limit}'")

        truncated = d.pop("truncated")

        obs_alert_collection_rejection = cls(
            scope=scope,
            reason=reason,
            totals=totals,
            items=items,
            total_items=total_items,
            limit=limit,
            truncated=truncated,
        )

        obs_alert_collection_rejection.additional_properties = d
        return obs_alert_collection_rejection

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
