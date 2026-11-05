from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugReplayRequest")


@_attrs_define
class DebugReplayRequest:
    """Optional target selection for a metadata-only debugger replay. Empty body preserves the default mirror rule selected
    for the retained request's serving deployment.

    """

    mirror_deployment_id: None | Unset | UUID = UNSET
    """Enabled mirror target deployment for the retained request's serving deployment."""

    def to_dict(self) -> dict[str, Any]:
        mirror_deployment_id: None | str | Unset
        if isinstance(self.mirror_deployment_id, Unset):
            mirror_deployment_id = UNSET
        elif isinstance(self.mirror_deployment_id, UUID):
            mirror_deployment_id = str(self.mirror_deployment_id)
        else:
            mirror_deployment_id = self.mirror_deployment_id

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if mirror_deployment_id is not UNSET:
            field_dict["mirror_deployment_id"] = mirror_deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_mirror_deployment_id(data: object) -> None | Unset | UUID:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                mirror_deployment_id_type_0 = UUID(data)

                return mirror_deployment_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | UUID, data)

        mirror_deployment_id = _parse_mirror_deployment_id(d.pop("mirror_deployment_id", UNSET))

        debug_replay_request = cls(
            mirror_deployment_id=mirror_deployment_id,
        )

        return debug_replay_request
