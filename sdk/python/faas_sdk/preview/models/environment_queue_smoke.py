from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.environment_queue_smoke_payload_type_0 import EnvironmentQueueSmokePayloadType0


T = TypeVar("T", bound="EnvironmentQueueSmoke")


@_attrs_define
class EnvironmentQueueSmoke:
    """Bounded customer-authored JSON payload used only for isolated push/pull-worker or HTTP-function qualification. Do
    not include secrets.

    """

    payload: bool | EnvironmentQueueSmokePayloadType0 | float | list[Any] | None | str
    """One JSON value, limited to 64 KiB, delivered to the candidate through its frozen queue invocation contract."""

    def to_dict(self) -> dict[str, Any]:
        from ..models.environment_queue_smoke_payload_type_0 import EnvironmentQueueSmokePayloadType0

        payload: bool | dict[str, Any] | float | list[Any] | None | str
        if isinstance(self.payload, EnvironmentQueueSmokePayloadType0):
            payload = self.payload.to_dict()
        elif isinstance(self.payload, list):
            payload = self.payload

        else:
            payload = self.payload

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "payload": payload,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_queue_smoke_payload_type_0 import EnvironmentQueueSmokePayloadType0

        d = dict(src_dict)

        def _parse_payload(data: object) -> bool | EnvironmentQueueSmokePayloadType0 | float | list[Any] | None | str:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                payload_type_0 = EnvironmentQueueSmokePayloadType0.from_dict(data)

                return payload_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, list):
                    raise TypeError()
                payload_type_1 = cast(list[Any], data)

                return payload_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(bool | EnvironmentQueueSmokePayloadType0 | float | list[Any] | None | str, data)

        payload = _parse_payload(d.pop("payload"))

        environment_queue_smoke = cls(
            payload=payload,
        )

        return environment_queue_smoke
