from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.tcp_listener_tls_status_response_scope import (
    TCPListenerTLSStatusResponseScope,
    check_tcp_listener_tls_status_response_scope,
)

if TYPE_CHECKING:
    from ..models.tcp_listener_tls_certificate_status import TCPListenerTLSCertificateStatus
    from ..models.tcp_listener_tls_config import TCPListenerTLSConfig


T = TypeVar("T", bound="TCPListenerTLSStatusResponse")


@_attrs_define
class TCPListenerTLSStatusResponse:
    """Certificate evidence from observed edges; empty observations mean unknown status, never fleet-wide readiness."""

    name: str
    tls: TCPListenerTLSConfig
    """Listener TLS intent. Termination requires a verified app-owned ASCII DNS hostname; passthrough forbids a
    hostname."""
    enabled: bool
    scope: TCPListenerTLSStatusResponseScope
    observations: list[TCPListenerTLSCertificateStatus]

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        tls = self.tls.to_dict()

        enabled = self.enabled

        scope: str = self.scope

        observations = []
        for observations_item_data in self.observations:
            observations_item = observations_item_data.to_dict()
            observations.append(observations_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "tls": tls,
                "enabled": enabled,
                "scope": scope,
                "observations": observations,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tcp_listener_tls_certificate_status import TCPListenerTLSCertificateStatus
        from ..models.tcp_listener_tls_config import TCPListenerTLSConfig

        d = dict(src_dict)
        name = d.pop("name")

        tls = TCPListenerTLSConfig.from_dict(d.pop("tls"))

        enabled = d.pop("enabled")

        scope = check_tcp_listener_tls_status_response_scope(d.pop("scope"))

        observations = []
        _observations = d.pop("observations")
        for observations_item_data in _observations:
            observations_item = TCPListenerTLSCertificateStatus.from_dict(observations_item_data)

            observations.append(observations_item)

        tcp_listener_tls_status_response = cls(
            name=name,
            tls=tls,
            enabled=enabled,
            scope=scope,
            observations=observations,
        )

        return tcp_listener_tls_status_response
