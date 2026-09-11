from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_rule_throttle_action_key_by import (
    EdgeRuleThrottleActionKeyBy,
    check_edge_rule_throttle_action_key_by,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleThrottleAction")


@_attrs_define
class EdgeRuleThrottleAction:
    """Per-route token-bucket cap (ADR-091 D20.5 amendment,
    issue #881). Customers tighten the per-route rps/burst
    below their plan's `plan.RateLimitRPS` — the apid
    validator enforces the sub-plan ceiling; the gateway
    compiler enforces it again at load time.

    Sub-plan ceiling — the load-bearing constraint. A
    throttle rule is STRICTLY a tightening primitive. A
    rule that exceeds the ceiling is rejected with 422
    BEFORE any DB write — a customer cannot raise their
    plan limit by registering a throttle rule.

    Per-IP sub-keying is deliberately absent in v1 — see
    the package doc on `pkg/state.EdgeRuleThrottleAction`
    for the design rationale (memory-bounded limiter +
    attacker-controlled IP cardinality = unbounded bucket
    growth).

    Phase 3 (ADR-091 D20.5 amendment 4, ADR-104, issue #881
    Phase 3) extends the wire shape with optional per-consumer
    keying. Default values (`""` for key_by, omitted
    jwt_claim_name, 0 for max_keys_per_rule) preserve PR
    #887's behaviour bit-for-bit. See ADR-104 for the policy
    and `pkg/gateway/ratelimit.go::AllowWithConsumerKey`
    (Phase 3) for the run-time semantics.

    """

    requests_per_second: float
    """Token-bucket refill rate per route. The apid
    validator rejects rps > plan.RateLimitRPS with a
    422. The gateway compiler clamps + warns on the
    same bound at load time.
    """
    burst: int
    """Token-bucket burst per route. Mirrors rps: rejected
    above `plan.RateLimitBurst` at create time and
    clamped at compile time.
    """
    key_by: EdgeRuleThrottleActionKeyBy | Unset = ""
    """Per-consumer keying dimension (ADR-104 / issue #881
    Phase 3). When `""` or `"none"`, the bucket is shared
    across every caller of the route (PR #887 shape).
    When `"api_key"`, one bucket per authenticated API
    key. When `"consumer_id"`, one bucket per stable API consumer
    identity (all rotated keys for that consumer share a bucket).
    When `"jwt_subject"`, one bucket per JWT `sub`.
    When `"jwt_claim"`, one bucket per value of the
    claim named by `jwt_claim_name`. Each non-empty
    value activates the bounded design: when the
    per-rule consumer set exceeds
    `max_keys_per_rule`, all over-cap callers collapse
    into a single non-evicting `__other__` bucket that
    still consumes tokens (the load-bearing safety
    property — see ADR-104 §"Consequences").
    """
    jwt_claim_name: str | Unset = UNSET
    """Required iff `key_by="jwt_claim"`. Names the JWT
    custom claim to extract (e.g., `"tier"`,
    `"org_id"`). Format is a CodeQL safe-identifier:
    leading letter or underscore, then `[A-Za-z0-9_]`,
    max 64 chars. Anything looser risks label-cardinality
    explosion in metric series or a CodeQL go-clear-
    text-logging finding on a future refactor.
    """
    max_keys_per_rule: int | Unset = 0
    """Caps the cardinality of the per-consumer bucket map
    for this rule. 0 means "use plan default"
    (`Limits.ThrottleMaxKeysPerRule` per plan: Free 100
    / Hobby 1000 / Pro 5000 / Scale 10000). The
    validator rejects values above
    `plan.ThrottleMaxKeysPerRule`. Must be 0 when
    `key_by` is `""` or `"none"` — the cap is moot for
    non-per-consumer rules.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        requests_per_second = self.requests_per_second

        burst = self.burst

        key_by: str | Unset = UNSET
        if not isinstance(self.key_by, Unset):
            key_by = self.key_by

        jwt_claim_name = self.jwt_claim_name

        max_keys_per_rule = self.max_keys_per_rule

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "requests_per_second": requests_per_second,
                "burst": burst,
            }
        )
        if key_by is not UNSET:
            field_dict["key_by"] = key_by
        if jwt_claim_name is not UNSET:
            field_dict["jwt_claim_name"] = jwt_claim_name
        if max_keys_per_rule is not UNSET:
            field_dict["max_keys_per_rule"] = max_keys_per_rule

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        requests_per_second = d.pop("requests_per_second")

        burst = d.pop("burst")

        _key_by = d.pop("key_by", UNSET)
        key_by: EdgeRuleThrottleActionKeyBy | Unset
        if isinstance(_key_by, Unset):
            key_by = UNSET
        else:
            key_by = check_edge_rule_throttle_action_key_by(_key_by)

        jwt_claim_name = d.pop("jwt_claim_name", UNSET)

        max_keys_per_rule = d.pop("max_keys_per_rule", UNSET)

        edge_rule_throttle_action = cls(
            requests_per_second=requests_per_second,
            burst=burst,
            key_by=key_by,
            jwt_claim_name=jwt_claim_name,
            max_keys_per_rule=max_keys_per_rule,
        )

        edge_rule_throttle_action.additional_properties = d
        return edge_rule_throttle_action

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
