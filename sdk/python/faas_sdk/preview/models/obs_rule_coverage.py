from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_rule_coverage_limitations_item import (
    ObsRuleCoverageLimitationsItem,
    check_obs_rule_coverage_limitations_item,
)
from ..models.obs_rule_coverage_reason import ObsRuleCoverageReason, check_obs_rule_coverage_reason
from ..models.obs_rule_coverage_selection_source import (
    ObsRuleCoverageSelectionSource,
    check_obs_rule_coverage_selection_source,
)
from ..models.obs_rule_coverage_state import ObsRuleCoverageState, check_obs_rule_coverage_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_rule_coverage_item import ObsRuleCoverageItem
    from ..models.obs_rule_coverage_set import ObsRuleCoverageSet
    from ..models.obs_rule_coverage_totals import ObsRuleCoverageTotals


T = TypeVar("T", bound="ObsRuleCoverage")


@_attrs_define
class ObsRuleCoverage:
    """Selected expected alert definitions compared with a complete current source; covered does not imply healthy
    evaluations or inputs.

    """

    generated_at: datetime.datetime
    scope: Literal["selected_expected_alert_rules"]
    fingerprint_algorithm: Literal["promql_ast_v1"]
    selection_source: ObsRuleCoverageSelectionSource
    state: ObsRuleCoverageState
    expected_set: None | ObsRuleCoverageSet
    """Selected versioned set metadata; null when no selection or its catalog is unavailable."""
    available_sets: list[ObsRuleCoverageSet]
    """Built-in choices from this binary; their presence is not deployment intent."""
    totals: None | ObsRuleCoverageTotals
    """Null for unselected or unreadable evidence; a complete empty source instead has known missing definitions."""
    items: list[ObsRuleCoverageItem]
    """Expected definitions ordered by ambiguity then change then absence then unknown then match; identity hashes
    break ties."""
    total_items: int
    limit: Literal[200]
    truncated: bool
    limitations: list[ObsRuleCoverageLimitationsItem]
    reason: ObsRuleCoverageReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.obs_rule_coverage_set import ObsRuleCoverageSet
        from ..models.obs_rule_coverage_totals import ObsRuleCoverageTotals

        generated_at = self.generated_at.isoformat()

        scope = self.scope

        fingerprint_algorithm = self.fingerprint_algorithm

        selection_source: str = self.selection_source

        state: str = self.state

        expected_set: dict[str, Any] | None
        if isinstance(self.expected_set, ObsRuleCoverageSet):
            expected_set = self.expected_set.to_dict()
        else:
            expected_set = self.expected_set

        available_sets = []
        for available_sets_item_data in self.available_sets:
            available_sets_item = available_sets_item_data.to_dict()
            available_sets.append(available_sets_item)

        totals: dict[str, Any] | None
        if isinstance(self.totals, ObsRuleCoverageTotals):
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

        limitations = []
        for limitations_item_data in self.limitations:
            limitations_item: str = limitations_item_data
            limitations.append(limitations_item)

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "generated_at": generated_at,
                "scope": scope,
                "fingerprint_algorithm": fingerprint_algorithm,
                "selection_source": selection_source,
                "state": state,
                "expected_set": expected_set,
                "available_sets": available_sets,
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
        from ..models.obs_rule_coverage_item import ObsRuleCoverageItem
        from ..models.obs_rule_coverage_set import ObsRuleCoverageSet
        from ..models.obs_rule_coverage_totals import ObsRuleCoverageTotals

        d = dict(src_dict)
        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        scope = cast(Literal["selected_expected_alert_rules"], d.pop("scope"))
        if scope != "selected_expected_alert_rules":
            raise ValueError(f"scope must match const 'selected_expected_alert_rules', got '{scope}'")

        fingerprint_algorithm = cast(Literal["promql_ast_v1"], d.pop("fingerprint_algorithm"))
        if fingerprint_algorithm != "promql_ast_v1":
            raise ValueError(f"fingerprint_algorithm must match const 'promql_ast_v1', got '{fingerprint_algorithm}'")

        selection_source = check_obs_rule_coverage_selection_source(d.pop("selection_source"))

        state = check_obs_rule_coverage_state(d.pop("state"))

        def _parse_expected_set(data: object) -> None | ObsRuleCoverageSet:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                expected_set_type_0 = ObsRuleCoverageSet.from_dict(data)

                return expected_set_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ObsRuleCoverageSet, data)

        expected_set = _parse_expected_set(d.pop("expected_set"))

        available_sets = []
        _available_sets = d.pop("available_sets")
        for available_sets_item_data in _available_sets:
            available_sets_item = ObsRuleCoverageSet.from_dict(available_sets_item_data)

            available_sets.append(available_sets_item)

        def _parse_totals(data: object) -> None | ObsRuleCoverageTotals:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                totals_type_0 = ObsRuleCoverageTotals.from_dict(data)

                return totals_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ObsRuleCoverageTotals, data)

        totals = _parse_totals(d.pop("totals"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuleCoverageItem.from_dict(items_item_data)

            items.append(items_item)

        total_items = d.pop("total_items")

        limit = cast(Literal[200], d.pop("limit"))
        if limit != 200:
            raise ValueError(f"limit must match const 200, got '{limit}'")

        truncated = d.pop("truncated")

        limitations = []
        _limitations = d.pop("limitations")
        for limitations_item_data in _limitations:
            limitations_item = check_obs_rule_coverage_limitations_item(limitations_item_data)

            limitations.append(limitations_item)

        _reason = d.pop("reason", UNSET)
        reason: ObsRuleCoverageReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_rule_coverage_reason(_reason)

        obs_rule_coverage = cls(
            generated_at=generated_at,
            scope=scope,
            fingerprint_algorithm=fingerprint_algorithm,
            selection_source=selection_source,
            state=state,
            expected_set=expected_set,
            available_sets=available_sets,
            totals=totals,
            items=items,
            total_items=total_items,
            limit=limit,
            truncated=truncated,
            limitations=limitations,
            reason=reason,
        )

        obs_rule_coverage.additional_properties = d
        return obs_rule_coverage

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
