from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_rule_health_response_state import ObsRuleHealthResponseState, check_obs_rule_health_response_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_rule_health_item import ObsRuleHealthItem
    from ..models.obs_rule_health_totals import ObsRuleHealthTotals


T = TypeVar("T", bound="ObsRuleHealthResponse")


@_attrs_define
class ObsRuleHealthResponse:
    """Point-in-time loaded-rule diagnostics with complete source totals and a bounded unhealthy-first preview."""

    generated_at: datetime.datetime
    scope: Literal["currently_loaded_alert_rules"]
    state: ObsRuleHealthResponseState
    max_age_seconds: Literal[120]
    clock_skew_tolerance_seconds: Literal[30]
    totals: None | ObsRuleHealthTotals
    """Null for unconfigured or failed source reads; a complete empty source has known zero rules and unavailable
    state."""
    items: list[ObsRuleHealthItem]
    """Invalid items precede invalid clocks then failures then stale then unavailable then healthy; group and rule
    hashes break ties."""
    total_items: int
    limit: Literal[200]
    truncated: bool
    limitations: list[str]
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.obs_rule_health_totals import ObsRuleHealthTotals

        generated_at = self.generated_at.isoformat()

        scope = self.scope

        state: str = self.state

        max_age_seconds = self.max_age_seconds

        clock_skew_tolerance_seconds = self.clock_skew_tolerance_seconds

        totals: dict[str, Any] | None
        if isinstance(self.totals, ObsRuleHealthTotals):
            totals = self.totals.to_dict()
        else:
            totals = self.totals

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        total_items = self.total_items

        limit = self.limit

        truncated = self.truncated

        limitations = self.limitations

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "generated_at": generated_at,
                "scope": scope,
                "state": state,
                "max_age_seconds": max_age_seconds,
                "clock_skew_tolerance_seconds": clock_skew_tolerance_seconds,
                "totals": totals,
                "items": items,
                "total_items": total_items,
                "limit": limit,
                "truncated": truncated,
                "limitations": limitations,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_rule_health_item import ObsRuleHealthItem
        from ..models.obs_rule_health_totals import ObsRuleHealthTotals

        d = dict(src_dict)
        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        scope = cast(Literal["currently_loaded_alert_rules"], d.pop("scope"))
        if scope != "currently_loaded_alert_rules":
            raise ValueError(f"scope must match const 'currently_loaded_alert_rules', got '{scope}'")

        state = check_obs_rule_health_response_state(d.pop("state"))

        max_age_seconds = cast(Literal[120], d.pop("max_age_seconds"))
        if max_age_seconds != 120:
            raise ValueError(f"max_age_seconds must match const 120, got '{max_age_seconds}'")

        clock_skew_tolerance_seconds = cast(Literal[30], d.pop("clock_skew_tolerance_seconds"))
        if clock_skew_tolerance_seconds != 30:
            raise ValueError(f"clock_skew_tolerance_seconds must match const 30, got '{clock_skew_tolerance_seconds}'")

        def _parse_totals(data: object) -> None | ObsRuleHealthTotals:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                totals_type_0 = ObsRuleHealthTotals.from_dict(data)

                return totals_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ObsRuleHealthTotals, data)

        totals = _parse_totals(d.pop("totals"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuleHealthItem.from_dict(items_item_data)

            items.append(items_item)

        total_items = d.pop("total_items")

        limit = cast(Literal[200], d.pop("limit"))
        if limit != 200:
            raise ValueError(f"limit must match const 200, got '{limit}'")

        truncated = d.pop("truncated")

        limitations = cast(list[str], d.pop("limitations"))

        reason = d.pop("reason", UNSET)

        obs_rule_health_response = cls(
            generated_at=generated_at,
            scope=scope,
            state=state,
            max_age_seconds=max_age_seconds,
            clock_skew_tolerance_seconds=clock_skew_tolerance_seconds,
            totals=totals,
            items=items,
            total_items=total_items,
            limit=limit,
            truncated=truncated,
            limitations=limitations,
            reason=reason,
        )

        obs_rule_health_response.additional_properties = d
        return obs_rule_health_response

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
