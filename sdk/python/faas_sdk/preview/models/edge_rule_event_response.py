from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_rule_event_response_outcome import (
    EdgeRuleEventResponseOutcome,
    check_edge_rule_event_response_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleEventResponse")


@_attrs_define
class EdgeRuleEventResponse:
    """One sampled request an edge rule matched, or a request a
    kind=waf rule detected as a likely attack.
    Detections carry the CRS rule IDs that scored, their attack
    categories and the anomaly score, never the matched payload.

    """

    id: str
    rule_id: str
    outcome: EdgeRuleEventResponseOutcome
    """matched (enforced rule) and logged (log-mode rule) are rule matches. detected is a kind=waf detection in a
    sampled request; warned and blocked are kind=waf detections in-path by a warn or block rule (blocked requests
    were answered with 403). challenged, rejected, solved and failed are kind=challenge outcomes: the challenge page
    served to a browser, a request that is not a browser navigation answered with 403, a solution that earned a
    pass, and a solution answered with 403. An enforced challenge rule records these instead of matched. boxed is a
    client put in a penalty box by the rule, recorded from the request whose refusal tipped it in."""
    occurred_at: datetime.datetime
    rule_name: str | Unset = UNSET
    """Empty once the rule is deleted."""
    rule_kind: str | Unset = UNSET
    request_id: str | Unset = UNSET
    method: str | Unset = UNSET
    host: str | Unset = UNSET
    path: str | Unset = UNSET
    """Request path without the query string."""
    client_ip: str | Unset = UNSET
    """Trusted client IP, when known."""
    country: str | Unset = UNSET
    user_agent: str | Unset = UNSET
    waf_rule_ids: list[int] | Unset = UNSET
    """CRS rule IDs that scored in a kind=waf detection."""
    waf_categories: list[str] | Unset = UNSET
    """CRS attack categories of a kind=waf detection (sqli, xss, lfi, ...)."""
    waf_score: int | Unset = UNSET
    """CRS inbound anomaly score of a kind=waf detection."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        rule_id = self.rule_id

        outcome: str = self.outcome

        occurred_at = self.occurred_at.isoformat()

        rule_name = self.rule_name

        rule_kind = self.rule_kind

        request_id = self.request_id

        method = self.method

        host = self.host

        path = self.path

        client_ip = self.client_ip

        country = self.country

        user_agent = self.user_agent

        waf_rule_ids: list[int] | Unset = UNSET
        if not isinstance(self.waf_rule_ids, Unset):
            waf_rule_ids = self.waf_rule_ids

        waf_categories: list[str] | Unset = UNSET
        if not isinstance(self.waf_categories, Unset):
            waf_categories = self.waf_categories

        waf_score = self.waf_score

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "rule_id": rule_id,
                "outcome": outcome,
                "occurred_at": occurred_at,
            }
        )
        if rule_name is not UNSET:
            field_dict["rule_name"] = rule_name
        if rule_kind is not UNSET:
            field_dict["rule_kind"] = rule_kind
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if method is not UNSET:
            field_dict["method"] = method
        if host is not UNSET:
            field_dict["host"] = host
        if path is not UNSET:
            field_dict["path"] = path
        if client_ip is not UNSET:
            field_dict["client_ip"] = client_ip
        if country is not UNSET:
            field_dict["country"] = country
        if user_agent is not UNSET:
            field_dict["user_agent"] = user_agent
        if waf_rule_ids is not UNSET:
            field_dict["waf_rule_ids"] = waf_rule_ids
        if waf_categories is not UNSET:
            field_dict["waf_categories"] = waf_categories
        if waf_score is not UNSET:
            field_dict["waf_score"] = waf_score

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        rule_id = d.pop("rule_id")

        outcome = check_edge_rule_event_response_outcome(d.pop("outcome"))

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        rule_name = d.pop("rule_name", UNSET)

        rule_kind = d.pop("rule_kind", UNSET)

        request_id = d.pop("request_id", UNSET)

        method = d.pop("method", UNSET)

        host = d.pop("host", UNSET)

        path = d.pop("path", UNSET)

        client_ip = d.pop("client_ip", UNSET)

        country = d.pop("country", UNSET)

        user_agent = d.pop("user_agent", UNSET)

        waf_rule_ids = cast(list[int], d.pop("waf_rule_ids", UNSET))

        waf_categories = cast(list[str], d.pop("waf_categories", UNSET))

        waf_score = d.pop("waf_score", UNSET)

        edge_rule_event_response = cls(
            id=id,
            rule_id=rule_id,
            outcome=outcome,
            occurred_at=occurred_at,
            rule_name=rule_name,
            rule_kind=rule_kind,
            request_id=request_id,
            method=method,
            host=host,
            path=path,
            client_ip=client_ip,
            country=country,
            user_agent=user_agent,
            waf_rule_ids=waf_rule_ids,
            waf_categories=waf_categories,
            waf_score=waf_score,
        )

        edge_rule_event_response.additional_properties = d
        return edge_rule_event_response

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
