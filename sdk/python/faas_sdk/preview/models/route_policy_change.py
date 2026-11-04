from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_edge_rule_request import CreateEdgeRuleRequest
    from ..models.route_policy_impact import RoutePolicyImpact
    from ..models.update_edge_rule_request import UpdateEdgeRuleRequest


T = TypeVar("T", bound="RoutePolicyChange")


@_attrs_define
class RoutePolicyChange:
    """Generated concrete or family selector creation or action-only update with expected outcome and affected traffic."""

    operation: str
    kind: str
    expected: str
    after: str
    impact: RoutePolicyImpact
    """Platform host, method and concrete or family path whose policy selection can change. Captured impact is
    bounded to the selected contract; wildcard rules can also affect uncaptured paths."""
    reason: str
    budget_group: str | Unset = UNSET
    """Declared group used for a consolidated budget selector."""
    rule_id: str | Unset = UNSET
    simulated_rule_id: str | Unset = UNSET
    create: CreateEdgeRuleRequest | Unset = UNSET
    """Body shape for POST /v1/apps/{slug}/edge-rules."""
    update: UpdateEdgeRuleRequest | Unset = UNSET
    """Partial update — every field optional. Kind is not patchable."""
    before: str | Unset = UNSET
    displaced_rule_ids: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        operation = self.operation

        kind = self.kind

        expected = self.expected

        after = self.after

        impact = self.impact.to_dict()

        reason = self.reason

        budget_group = self.budget_group

        rule_id = self.rule_id

        simulated_rule_id = self.simulated_rule_id

        create: dict[str, Any] | Unset = UNSET
        if not isinstance(self.create, Unset):
            create = self.create.to_dict()

        update: dict[str, Any] | Unset = UNSET
        if not isinstance(self.update, Unset):
            update = self.update.to_dict()

        before = self.before

        displaced_rule_ids: list[str] | Unset = UNSET
        if not isinstance(self.displaced_rule_ids, Unset):
            displaced_rule_ids = self.displaced_rule_ids

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "operation": operation,
                "kind": kind,
                "expected": expected,
                "after": after,
                "impact": impact,
                "reason": reason,
            }
        )
        if budget_group is not UNSET:
            field_dict["budget_group"] = budget_group
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if simulated_rule_id is not UNSET:
            field_dict["simulated_rule_id"] = simulated_rule_id
        if create is not UNSET:
            field_dict["create"] = create
        if update is not UNSET:
            field_dict["update"] = update
        if before is not UNSET:
            field_dict["before"] = before
        if displaced_rule_ids is not UNSET:
            field_dict["displaced_rule_ids"] = displaced_rule_ids

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_edge_rule_request import CreateEdgeRuleRequest
        from ..models.route_policy_impact import RoutePolicyImpact
        from ..models.update_edge_rule_request import UpdateEdgeRuleRequest

        d = dict(src_dict)
        operation = d.pop("operation")

        kind = d.pop("kind")

        expected = d.pop("expected")

        after = d.pop("after")

        impact = RoutePolicyImpact.from_dict(d.pop("impact"))

        reason = d.pop("reason")

        budget_group = d.pop("budget_group", UNSET)

        rule_id = d.pop("rule_id", UNSET)

        simulated_rule_id = d.pop("simulated_rule_id", UNSET)

        _create = d.pop("create", UNSET)
        create: CreateEdgeRuleRequest | Unset
        if isinstance(_create, Unset):
            create = UNSET
        else:
            create = CreateEdgeRuleRequest.from_dict(_create)

        _update = d.pop("update", UNSET)
        update: UpdateEdgeRuleRequest | Unset
        if isinstance(_update, Unset):
            update = UNSET
        else:
            update = UpdateEdgeRuleRequest.from_dict(_update)

        before = d.pop("before", UNSET)

        displaced_rule_ids = cast(list[str], d.pop("displaced_rule_ids", UNSET))

        route_policy_change = cls(
            operation=operation,
            kind=kind,
            expected=expected,
            after=after,
            impact=impact,
            reason=reason,
            budget_group=budget_group,
            rule_id=rule_id,
            simulated_rule_id=simulated_rule_id,
            create=create,
            update=update,
            before=before,
            displaced_rule_ids=displaced_rule_ids,
        )

        route_policy_change.additional_properties = d
        return route_policy_change

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
