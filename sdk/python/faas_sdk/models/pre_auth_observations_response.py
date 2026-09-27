from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.pre_auth_observations_response_range import (
    PreAuthObservationsResponseRange,
    check_pre_auth_observations_response_range,
)

if TYPE_CHECKING:
    from ..models.pre_auth_policy_observation import PreAuthPolicyObservation


T = TypeVar("T", bound="PreAuthObservationsResponse")


@_attrs_define
class PreAuthObservationsResponse:
    app_id: str
    range_: PreAuthObservationsResponseRange
    source: str
    """prometheus or degraded: <reason>."""
    as_of: datetime.datetime
    policies: list[PreAuthPolicyObservation]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        range_: str = self.range_

        source = self.source

        as_of = self.as_of.isoformat()

        policies = []
        for policies_item_data in self.policies:
            policies_item = policies_item_data.to_dict()
            policies.append(policies_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "range": range_,
                "source": source,
                "as_of": as_of,
                "policies": policies,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.pre_auth_policy_observation import PreAuthPolicyObservation

        d = dict(src_dict)
        app_id = d.pop("app_id")

        range_ = check_pre_auth_observations_response_range(d.pop("range"))

        source = d.pop("source")

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        policies = []
        _policies = d.pop("policies")
        for policies_item_data in _policies:
            policies_item = PreAuthPolicyObservation.from_dict(policies_item_data)

            policies.append(policies_item)

        pre_auth_observations_response = cls(
            app_id=app_id,
            range_=range_,
            source=source,
            as_of=as_of,
            policies=policies,
        )

        pre_auth_observations_response.additional_properties = d
        return pre_auth_observations_response

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
