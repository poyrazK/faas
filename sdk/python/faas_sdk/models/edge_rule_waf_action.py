from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_rule_waf_action_mode import EdgeRuleWAFActionMode, check_edge_rule_waf_action_mode
from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleWAFAction")


@_attrs_define
class EdgeRuleWAFAction:
    """Inspects matched requests with the OWASP Core Rule Set (ADR-831).
    Every mode samples requests (bodies included) off the request path
    and reports detections without blocking them. warn and block also
    check headers and URL in-path with a smaller rule set at paranoia
    level 1: warn tags a detected request's response with X-WAF-Warning,
    block answers it with 403. Request bodies are never blocked. When the
    app's in-path budget is exhausted a request passes unchecked (counted
    as inline_skipped). Detections appear in the edge-protection summary
    and the edge_waf_detections alert preset. Pro and above. Omitted
    values are stored as their defaults.

    """

    mode: EdgeRuleWAFActionMode | Unset = "observe"
    """warn and block require paranoia_level 1."""
    paranoia_level: int | Unset = 1
    """CRS paranoia level. Level 2 detects more and produces more false positives."""
    anomaly_threshold: int | Unset = 5
    """Inbound anomaly score at which a request counts as a detection (one critical match scores 5)."""
    exclude_rule_ids: list[int] | Unset = UNSET
    """CRS rule IDs left out of scoring on this route, for known false positives."""
    inspect_body_bytes: int | Unset = 8192
    """Request body prefix inspected. Larger and streaming bodies are inspected up to this size only; larger values
    use more of the app's inspection budget."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mode: str | Unset = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode

        paranoia_level = self.paranoia_level

        anomaly_threshold = self.anomaly_threshold

        exclude_rule_ids: list[int] | Unset = UNSET
        if not isinstance(self.exclude_rule_ids, Unset):
            exclude_rule_ids = self.exclude_rule_ids

        inspect_body_bytes = self.inspect_body_bytes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if mode is not UNSET:
            field_dict["mode"] = mode
        if paranoia_level is not UNSET:
            field_dict["paranoia_level"] = paranoia_level
        if anomaly_threshold is not UNSET:
            field_dict["anomaly_threshold"] = anomaly_threshold
        if exclude_rule_ids is not UNSET:
            field_dict["exclude_rule_ids"] = exclude_rule_ids
        if inspect_body_bytes is not UNSET:
            field_dict["inspect_body_bytes"] = inspect_body_bytes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _mode = d.pop("mode", UNSET)
        mode: EdgeRuleWAFActionMode | Unset
        if isinstance(_mode, Unset):
            mode = UNSET
        else:
            mode = check_edge_rule_waf_action_mode(_mode)

        paranoia_level = d.pop("paranoia_level", UNSET)

        anomaly_threshold = d.pop("anomaly_threshold", UNSET)

        exclude_rule_ids = cast(list[int], d.pop("exclude_rule_ids", UNSET))

        inspect_body_bytes = d.pop("inspect_body_bytes", UNSET)

        edge_rule_waf_action = cls(
            mode=mode,
            paranoia_level=paranoia_level,
            anomaly_threshold=anomaly_threshold,
            exclude_rule_ids=exclude_rule_ids,
            inspect_body_bytes=inspect_body_bytes,
        )

        edge_rule_waf_action.additional_properties = d
        return edge_rule_waf_action

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
