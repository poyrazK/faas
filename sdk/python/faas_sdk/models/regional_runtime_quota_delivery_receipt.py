from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.regional_runtime_quota_envelope import RegionalRuntimeQuotaEnvelope


T = TypeVar("T", bound="RegionalRuntimeQuotaDeliveryReceipt")


@_attrs_define
class RegionalRuntimeQuotaDeliveryReceipt:
    """At least one original envelope is present. Receipt storage is independent of frozen admission and native drain
    acknowledgement.

    """

    grant: None | RegionalRuntimeQuotaEnvelope
    retirement: None | RegionalRuntimeQuotaEnvelope
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.regional_runtime_quota_envelope import RegionalRuntimeQuotaEnvelope

        grant: dict[str, Any] | None
        if isinstance(self.grant, RegionalRuntimeQuotaEnvelope):
            grant = self.grant.to_dict()
        else:
            grant = self.grant

        retirement: dict[str, Any] | None
        if isinstance(self.retirement, RegionalRuntimeQuotaEnvelope):
            retirement = self.retirement.to_dict()
        else:
            retirement = self.retirement

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "grant": grant,
                "retirement": retirement,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.regional_runtime_quota_envelope import RegionalRuntimeQuotaEnvelope

        d = dict(src_dict)

        def _parse_grant(data: object) -> None | RegionalRuntimeQuotaEnvelope:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                grant_type_0 = RegionalRuntimeQuotaEnvelope.from_dict(data)

                return grant_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | RegionalRuntimeQuotaEnvelope, data)

        grant = _parse_grant(d.pop("grant"))

        def _parse_retirement(data: object) -> None | RegionalRuntimeQuotaEnvelope:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                retirement_type_0 = RegionalRuntimeQuotaEnvelope.from_dict(data)

                return retirement_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | RegionalRuntimeQuotaEnvelope, data)

        retirement = _parse_retirement(d.pop("retirement"))

        regional_runtime_quota_delivery_receipt = cls(
            grant=grant,
            retirement=retirement,
        )

        regional_runtime_quota_delivery_receipt.additional_properties = d
        return regional_runtime_quota_delivery_receipt

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
