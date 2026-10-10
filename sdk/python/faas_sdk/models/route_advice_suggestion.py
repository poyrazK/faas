from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_advice_suggestion_kind import RouteAdviceSuggestionKind, check_route_advice_suggestion_kind
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_edge_rule_request import CreateEdgeRuleRequest
    from ..models.route_advice_evidence import RouteAdviceEvidence
    from ..models.route_advice_impact import RouteAdviceImpact


T = TypeVar("T", bound="RouteAdviceSuggestion")


@_attrs_define
class RouteAdviceSuggestion:
    """One proposed edge rule for one observed route, with evidence, a what-if estimate and disabled rule bodies (one per
    hostname).

    """

    id: str
    """Stable for the same kind, method and route."""
    kind: RouteAdviceSuggestionKind
    method: str
    route: str
    """Observed route template, for example /users/{id}."""
    title: str
    rationale: str
    evidence: RouteAdviceEvidence
    """Observed traffic for the route over the window. Consumer fields are present only on throttle suggestions."""
    impact: RouteAdviceImpact
    """What-if estimate replayed from aggregated telemetry over the same window; observed_only, not a guarantee."""
    rules: list[CreateEdgeRuleRequest]
    cautions: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        kind: str = self.kind

        method = self.method

        route = self.route

        title = self.title

        rationale = self.rationale

        evidence = self.evidence.to_dict()

        impact = self.impact.to_dict()

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        cautions: list[str] | Unset = UNSET
        if not isinstance(self.cautions, Unset):
            cautions = self.cautions

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "kind": kind,
                "method": method,
                "route": route,
                "title": title,
                "rationale": rationale,
                "evidence": evidence,
                "impact": impact,
                "rules": rules,
            }
        )
        if cautions is not UNSET:
            field_dict["cautions"] = cautions

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_edge_rule_request import CreateEdgeRuleRequest
        from ..models.route_advice_evidence import RouteAdviceEvidence
        from ..models.route_advice_impact import RouteAdviceImpact

        d = dict(src_dict)
        id = d.pop("id")

        kind = check_route_advice_suggestion_kind(d.pop("kind"))

        method = d.pop("method")

        route = d.pop("route")

        title = d.pop("title")

        rationale = d.pop("rationale")

        evidence = RouteAdviceEvidence.from_dict(d.pop("evidence"))

        impact = RouteAdviceImpact.from_dict(d.pop("impact"))

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = CreateEdgeRuleRequest.from_dict(rules_item_data)

            rules.append(rules_item)

        cautions = cast(list[str], d.pop("cautions", UNSET))

        route_advice_suggestion = cls(
            id=id,
            kind=kind,
            method=method,
            route=route,
            title=title,
            rationale=rationale,
            evidence=evidence,
            impact=impact,
            rules=rules,
            cautions=cautions,
        )

        route_advice_suggestion.additional_properties = d
        return route_advice_suggestion

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
