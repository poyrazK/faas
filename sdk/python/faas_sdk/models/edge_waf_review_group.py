from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_waf_review_group_assessment import (
    EdgeWAFReviewGroupAssessment,
    check_edge_waf_review_group_assessment,
)
from ..models.edge_waf_review_group_rule_mode import EdgeWAFReviewGroupRuleMode, check_edge_waf_review_group_rule_mode
from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeWAFReviewGroup")


@_attrs_define
class EdgeWAFReviewGroup:
    """One edge rule's detections by one CRS rule on one path."""

    rule_id: str
    crs_rule_id: int
    category: str
    """CRS attack category of the rule (sqli, xss, rce, lfi, rfi, php, java, generic, protocol, multipart, scanner,
    session_fixation, other)."""
    path: str
    """Request path with id-like segments (numbers, UUIDs, long hex or tokens) read as {id}."""
    events: int
    detected: int
    """Detections in sampled requests."""
    warned: int
    blocked: int
    clients: int
    """Distinct client IPs."""
    top_client_percent: int
    """Share of the group's detections from its busiest client."""
    scanner_percent: int
    """Share of the group's detections from clients flagged on 5 or more paths in the window."""
    first_seen: datetime.datetime
    last_seen: datetime.datetime
    request_ids: list[str]
    """The newest example request IDs; look them up with the events listing."""
    excluded: bool
    """The rule excludes this CRS rule now."""
    assessment: EdgeWAFReviewGroupAssessment
    reason: str
    """Why the group got its assessment."""
    rule_name: str | Unset = UNSET
    rule_mode: EdgeWAFReviewGroupRuleMode | Unset = UNSET
    known_false_positive: str | Unset = UNSET
    """The documented legitimate traffic this CRS rule flags."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rule_id = self.rule_id

        crs_rule_id = self.crs_rule_id

        category = self.category

        path = self.path

        events = self.events

        detected = self.detected

        warned = self.warned

        blocked = self.blocked

        clients = self.clients

        top_client_percent = self.top_client_percent

        scanner_percent = self.scanner_percent

        first_seen = self.first_seen.isoformat()

        last_seen = self.last_seen.isoformat()

        request_ids = self.request_ids

        excluded = self.excluded

        assessment: str = self.assessment

        reason = self.reason

        rule_name = self.rule_name

        rule_mode: str | Unset = UNSET
        if not isinstance(self.rule_mode, Unset):
            rule_mode = self.rule_mode

        known_false_positive = self.known_false_positive

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rule_id": rule_id,
                "crs_rule_id": crs_rule_id,
                "category": category,
                "path": path,
                "events": events,
                "detected": detected,
                "warned": warned,
                "blocked": blocked,
                "clients": clients,
                "top_client_percent": top_client_percent,
                "scanner_percent": scanner_percent,
                "first_seen": first_seen,
                "last_seen": last_seen,
                "request_ids": request_ids,
                "excluded": excluded,
                "assessment": assessment,
                "reason": reason,
            }
        )
        if rule_name is not UNSET:
            field_dict["rule_name"] = rule_name
        if rule_mode is not UNSET:
            field_dict["rule_mode"] = rule_mode
        if known_false_positive is not UNSET:
            field_dict["known_false_positive"] = known_false_positive

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rule_id = d.pop("rule_id")

        crs_rule_id = d.pop("crs_rule_id")

        category = d.pop("category")

        path = d.pop("path")

        events = d.pop("events")

        detected = d.pop("detected")

        warned = d.pop("warned")

        blocked = d.pop("blocked")

        clients = d.pop("clients")

        top_client_percent = d.pop("top_client_percent")

        scanner_percent = d.pop("scanner_percent")

        first_seen = datetime.datetime.fromisoformat(d.pop("first_seen"))

        last_seen = datetime.datetime.fromisoformat(d.pop("last_seen"))

        request_ids = cast(list[str], d.pop("request_ids"))

        excluded = d.pop("excluded")

        assessment = check_edge_waf_review_group_assessment(d.pop("assessment"))

        reason = d.pop("reason")

        rule_name = d.pop("rule_name", UNSET)

        _rule_mode = d.pop("rule_mode", UNSET)
        rule_mode: EdgeWAFReviewGroupRuleMode | Unset
        if isinstance(_rule_mode, Unset):
            rule_mode = UNSET
        else:
            rule_mode = check_edge_waf_review_group_rule_mode(_rule_mode)

        known_false_positive = d.pop("known_false_positive", UNSET)

        edge_waf_review_group = cls(
            rule_id=rule_id,
            crs_rule_id=crs_rule_id,
            category=category,
            path=path,
            events=events,
            detected=detected,
            warned=warned,
            blocked=blocked,
            clients=clients,
            top_client_percent=top_client_percent,
            scanner_percent=scanner_percent,
            first_seen=first_seen,
            last_seen=last_seen,
            request_ids=request_ids,
            excluded=excluded,
            assessment=assessment,
            reason=reason,
            rule_name=rule_name,
            rule_mode=rule_mode,
            known_false_positive=known_false_positive,
        )

        edge_waf_review_group.additional_properties = d
        return edge_waf_review_group

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
