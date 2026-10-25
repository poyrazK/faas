from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleMaintenanceAction")


@_attrs_define
class EdgeRuleMaintenanceAction:
    """Per-route maintenance toggle (ADR-091 amendment, §4.1.2.13).
    The fine-grained primitive for "this endpoint is in
    maintenance mode" — the hot-path applier
    (pkg/gateway.(*Handler).applyEdgeRuleMaintenance) short-
    circuits a matched (host, path, http_method) request with
    503 + Retry-After BEFORE auth, BEFORE wake. The coarse
    sibling (`apps.maintenance_mode`, PATCH /v1/apps/{slug})
    covers the "whole app in maintenance" case; use this kind
    when only specific routes need to be pinned.

    Field-by-field:
      * `retry_after_seconds` — optional per-rule override for
        the `Retry-After` header. 0 (default) = use the
        platform default
        `api.EdgeRuleMaintenanceRetryAfterSeconds` (60 s).
        Must be in `[0, 86400]` (24 h, enforced by
        `api.MaxEdgeRuleMaintenanceRetryAfterSeconds`); a
        customer cannot ship a rule that asks a client to
        back off for a week.
      * `message` — optional operator-friendly string that
        goes into `Problem.detail`. ≤ 512 B (same payload-
        size budget as `EdgeRuleValidateAction.schema`).
        Surface this on the customer's status page or in a
        dashboard alert so operators see why the endpoint is
        dark without scraping the rule row.

    Free-and-above (no plan gate). Mirror the validate /
    limit posture: rejection never reaches the wake gate, the
    auth chain, or the rate limiter.

    """

    retry_after_seconds: int | Unset = UNSET
    """Optional per-rule Retry-After override in seconds.
    0 (default) = use the platform default
    `api.EdgeRuleMaintenanceRetryAfterSeconds` (60 s).
    Must be in `[0, 86400]` (24 h); values above 86400 are
    rejected at create-time with 422.
    """
    message: str | Unset = UNSET
    """Optional operator-friendly detail string that goes into
    `Problem.detail`. ≤ 512 B. Surface this on the
    customer's status page so monitoring / curl users see
    why the endpoint is dark.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        retry_after_seconds = self.retry_after_seconds

        message = self.message

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if retry_after_seconds is not UNSET:
            field_dict["retry_after_seconds"] = retry_after_seconds
        if message is not UNSET:
            field_dict["message"] = message

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        retry_after_seconds = d.pop("retry_after_seconds", UNSET)

        message = d.pop("message", UNSET)

        edge_rule_maintenance_action = cls(
            retry_after_seconds=retry_after_seconds,
            message=message,
        )

        edge_rule_maintenance_action.additional_properties = d
        return edge_rule_maintenance_action

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
