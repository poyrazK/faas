from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.alert_anomaly_deployment_promotion import AlertAnomalyDeploymentPromotion


T = TypeVar("T", bound="AlertAnomalyDeploymentContext")


@_attrs_define
class AlertAnomalyDeploymentContext:
    """Recorded full-traffic promotions for the selected environment. An app-wide replay includes default-environment
    promotions.

    """

    scope: str
    """Deployment environment scope."""
    promotions: list[AlertAnomalyDeploymentPromotion]
    """Recorded promotions, newest first. Includes up to 100 in the selected two hours and the latest one before
    the window when available."""
    truncated: bool | Unset = UNSET
    """True when more than 100 recorded promotions fall within the replay window."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope = self.scope

        promotions = []
        for promotions_item_data in self.promotions:
            promotions_item = promotions_item_data.to_dict()
            promotions.append(promotions_item)

        truncated = self.truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "promotions": promotions,
            }
        )
        if truncated is not UNSET:
            field_dict["truncated"] = truncated

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.alert_anomaly_deployment_promotion import AlertAnomalyDeploymentPromotion

        d = dict(src_dict)
        scope = d.pop("scope")

        promotions = []
        _promotions = d.pop("promotions")
        for promotions_item_data in _promotions:
            promotions_item = AlertAnomalyDeploymentPromotion.from_dict(promotions_item_data)

            promotions.append(promotions_item)

        truncated = d.pop("truncated", UNSET)

        alert_anomaly_deployment_context = cls(
            scope=scope,
            promotions=promotions,
            truncated=truncated,
        )

        alert_anomaly_deployment_context.additional_properties = d
        return alert_anomaly_deployment_context

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
