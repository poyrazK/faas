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
    policy_id: str
    """Bounded app, route_<index>, or failures_<index> policy identifier. Index is the route's current array
    position."""
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
