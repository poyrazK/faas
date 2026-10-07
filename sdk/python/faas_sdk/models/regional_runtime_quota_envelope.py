from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="RegionalRuntimeQuotaEnvelope")


@_attrs_define
class RegionalRuntimeQuotaEnvelope:
    """Canonical signed directory envelope. Existing quota envelopes are bounded to 8192 bytes; payloads to 4096 bytes.
    This transport never authorizes native execution.

    """

    key_id: str
    payload: str
    signature: str

    def to_dict(self) -> dict[str, Any]:
        key_id = self.key_id

        payload = self.payload

        signature = self.signature

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key_id": key_id,
                "payload": payload,
                "signature": signature,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key_id = d.pop("key_id")

        payload = d.pop("payload")

        signature = d.pop("signature")

        regional_runtime_quota_envelope = cls(
            key_id=key_id,
            payload=payload,
            signature=signature,
        )

        return regional_runtime_quota_envelope
