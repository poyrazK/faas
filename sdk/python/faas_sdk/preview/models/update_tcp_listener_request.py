from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.tcp_listener_tls_config import TCPListenerTLSConfig


T = TypeVar("T", bound="UpdateTCPListenerRequest")


@_attrs_define
class UpdateTCPListenerRequest:
    """Supply exactly one serving-state or TLS-policy mutation. TLS changes disable the listener."""

    enabled: bool | Unset = UNSET
    tls: TCPListenerTLSConfig | Unset = UNSET
    """Listener TLS intent. Termination requires a verified app-owned ASCII DNS hostname; passthrough forbids a
    hostname."""

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        tls: dict[str, Any] | Unset = UNSET
        if not isinstance(self.tls, Unset):
            tls = self.tls.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if tls is not UNSET:
            field_dict["tls"] = tls

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tcp_listener_tls_config import TCPListenerTLSConfig

        d = dict(src_dict)
        enabled = d.pop("enabled", UNSET)

        _tls = d.pop("tls", UNSET)
        tls: TCPListenerTLSConfig | Unset
        if isinstance(_tls, Unset):
            tls = UNSET
        else:
            tls = TCPListenerTLSConfig.from_dict(_tls)

        update_tcp_listener_request = cls(
            enabled=enabled,
            tls=tls,
        )

        return update_tcp_listener_request
