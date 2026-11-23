from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AlertAnomalyDeploymentPromotion")


@_attrs_define
class AlertAnomalyDeploymentPromotion:
    """A recorded change that sent all traffic in an environment to a deployment."""

    deployment_id: UUID
    promoted_at: datetime.datetime
    """When a traffic change last took this deployment to 100 percent of the scope."""
    commit_sha: str | Unset = UNSET
    """Upstream commit SHA when recorded."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        promoted_at = self.promoted_at.isoformat()

        commit_sha = self.commit_sha

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "promoted_at": promoted_at,
            }
        )
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        promoted_at = datetime.datetime.fromisoformat(d.pop("promoted_at"))

        commit_sha = d.pop("commit_sha", UNSET)

        alert_anomaly_deployment_promotion = cls(
            deployment_id=deployment_id,
            promoted_at=promoted_at,
            commit_sha=commit_sha,
        )

        alert_anomaly_deployment_promotion.additional_properties = d
        return alert_anomaly_deployment_promotion

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
