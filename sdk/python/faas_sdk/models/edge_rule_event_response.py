from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

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
    """One sampled request an edge rule matched (ADR-908)."""

    id: str
    rule_id: str
    outcome: EdgeRuleEventResponseOutcome
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
