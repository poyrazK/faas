from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.regional_runtime_quota_binding import RegionalRuntimeQuotaBinding
    from ..models.regional_runtime_quota_envelope import RegionalRuntimeQuotaEnvelope


T = TypeVar("T", bound="RegionalRuntimeQuotaDelivery")


@_attrs_define
class RegionalRuntimeQuotaDelivery:
    """Canonical delivery body bounded to 20480 bytes. The direct grant and retirement routes reject consent_id; consent-
    based delivery uses the delegated receiver.

    """

    binding: RegionalRuntimeQuotaBinding
    """Exact original quota wire binding. Capitalized field spelling and an empty RegionalOrgID are retained for
    canonical transport compatibility."""
    authority: RegionalRuntimeQuotaEnvelope
    """Canonical signed directory envelope. Existing quota envelopes are bounded to 8192 bytes; payloads to 4096
    bytes. This transport never authorizes native execution."""
    consent_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        binding = self.binding.to_dict()

        authority = self.authority.to_dict()

        consent_id: str | Unset = UNSET
        if not isinstance(self.consent_id, Unset):
            consent_id = str(self.consent_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "binding": binding,
                "authority": authority,
            }
        )
        if consent_id is not UNSET:
            field_dict["consent_id"] = consent_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.regional_runtime_quota_binding import RegionalRuntimeQuotaBinding
        from ..models.regional_runtime_quota_envelope import RegionalRuntimeQuotaEnvelope

        d = dict(src_dict)
        binding = RegionalRuntimeQuotaBinding.from_dict(d.pop("binding"))

        authority = RegionalRuntimeQuotaEnvelope.from_dict(d.pop("authority"))

        _consent_id = d.pop("consent_id", UNSET)
        consent_id: UUID | Unset
        if isinstance(_consent_id, Unset):
            consent_id = UNSET
        else:
            consent_id = UUID(_consent_id)

        regional_runtime_quota_delivery = cls(
            binding=binding,
            authority=authority,
            consent_id=consent_id,
        )

        return regional_runtime_quota_delivery
