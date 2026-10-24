from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.tcp_listener_tls_config_mode import TCPListenerTLSConfigMode, check_tcp_listener_tls_config_mode
from ..types import UNSET, Unset

T = TypeVar("T", bound="TCPListenerTLSConfig")


@_attrs_define
class TCPListenerTLSConfig:
    """Listener TLS intent. Termination requires a verified app-owned ASCII DNS hostname; passthrough forbids a hostname."""

    mode: TCPListenerTLSConfigMode = "passthrough"
    hostname: str | Unset = UNSET
    """Normalized DNS hostname for termination. IP-shaped names and wildcards are forbidden."""

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        hostname = self.hostname

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "mode": mode,
            }
        )
        if hostname is not UNSET:
            field_dict["hostname"] = hostname

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_tcp_listener_tls_config_mode(d.pop("mode"))

        hostname = d.pop("hostname", UNSET)

        tcp_listener_tls_config = cls(
            mode=mode,
            hostname=hostname,
        )

        return tcp_listener_tls_config
