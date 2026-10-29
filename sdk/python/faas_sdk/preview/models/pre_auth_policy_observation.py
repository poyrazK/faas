from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.pre_auth_policy_observation_kind import (
    PreAuthPolicyObservationKind,
    check_pre_auth_policy_observation_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PreAuthPolicyObservation")


@_attrs_define
class PreAuthPolicyObservation:
    """Counts for one bounded app, route, failed-response, or target-observation policy slot."""

    policy_id: str
    """Bounded app, route_<index>, failures_<index>, or targets_<index> policy identifier. Index is the route's
    current array position."""
    kind: PreAuthPolicyObservationKind
    would_block: int
    result_2xx: int
    result_3xx: int
    result_4xx: int
    result_5xx: int
    result_unknown: int
    method: str | Unset = UNSET
    """Configured public method; omitted for the app policy."""
    path: str | Unset = UNSET
    """Configured public path; omitted for the app policy."""
    target_failures: int | Unset = UNSET
    """Selected app failures carrying a valid target identifier."""
    target_threshold: int | Unset = UNSET
    """Failures for which both bounded target shards exceeded the configured failed-response budget. Approximate
    signal; never blocks."""
    target_missing: int | Unset = UNSET
    """Selected app failures without a target header."""
    target_invalid: int | Unset = UNSET
    """Selected app failures with an invalid target header."""
    target_fallback: int | Unset = UNSET
    """Valid target failures observed with process-local fallback because central coordination was unavailable."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        policy_id = self.policy_id

        kind: str = self.kind

        would_block = self.would_block

        result_2xx = self.result_2xx

        result_3xx = self.result_3xx

        result_4xx = self.result_4xx

        result_5xx = self.result_5xx

        result_unknown = self.result_unknown

        method = self.method

        path = self.path

        target_failures = self.target_failures

        target_threshold = self.target_threshold

        target_missing = self.target_missing

        target_invalid = self.target_invalid

        target_fallback = self.target_fallback

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "policy_id": policy_id,
                "kind": kind,
                "would_block": would_block,
                "result_2xx": result_2xx,
                "result_3xx": result_3xx,
                "result_4xx": result_4xx,
                "result_5xx": result_5xx,
                "result_unknown": result_unknown,
            }
        )
        if method is not UNSET:
            field_dict["method"] = method
        if path is not UNSET:
            field_dict["path"] = path
        if target_failures is not UNSET:
            field_dict["target_failures"] = target_failures
        if target_threshold is not UNSET:
            field_dict["target_threshold"] = target_threshold
        if target_missing is not UNSET:
            field_dict["target_missing"] = target_missing
        if target_invalid is not UNSET:
            field_dict["target_invalid"] = target_invalid
        if target_fallback is not UNSET:
            field_dict["target_fallback"] = target_fallback

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        policy_id = d.pop("policy_id")

        kind = check_pre_auth_policy_observation_kind(d.pop("kind"))

        would_block = d.pop("would_block")

        result_2xx = d.pop("result_2xx")

        result_3xx = d.pop("result_3xx")

        result_4xx = d.pop("result_4xx")

        result_5xx = d.pop("result_5xx")

        result_unknown = d.pop("result_unknown")

        method = d.pop("method", UNSET)

        path = d.pop("path", UNSET)

        target_failures = d.pop("target_failures", UNSET)

        target_threshold = d.pop("target_threshold", UNSET)

        target_missing = d.pop("target_missing", UNSET)

        target_invalid = d.pop("target_invalid", UNSET)

        target_fallback = d.pop("target_fallback", UNSET)

        pre_auth_policy_observation = cls(
            policy_id=policy_id,
            kind=kind,
            would_block=would_block,
            result_2xx=result_2xx,
            result_3xx=result_3xx,
            result_4xx=result_4xx,
            result_5xx=result_5xx,
            result_unknown=result_unknown,
            method=method,
            path=path,
            target_failures=target_failures,
            target_threshold=target_threshold,
            target_missing=target_missing,
            target_invalid=target_invalid,
            target_fallback=target_fallback,
        )

        pre_auth_policy_observation.additional_properties = d
        return pre_auth_policy_observation

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
