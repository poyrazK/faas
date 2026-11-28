from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.tcp_listener_tls_certificate_status_status import (
    TCPListenerTLSCertificateStatusStatus,
    check_tcp_listener_tls_certificate_status_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="TCPListenerTLSCertificateStatus")


@_attrs_define
class TCPListenerTLSCertificateStatus:
    """Certificate evidence for one edge. Unknown evidence omits certificate expiry."""

    edge_id: str
    status: TCPListenerTLSCertificateStatusStatus
    observed_at: datetime.datetime
    not_after: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        edge_id = self.edge_id

        status: str = self.status

        observed_at = self.observed_at.isoformat()

        not_after: str | Unset = UNSET
        if not isinstance(self.not_after, Unset):
            not_after = self.not_after.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "edge_id": edge_id,
                "status": status,
                "observed_at": observed_at,
            }
        )
        if not_after is not UNSET:
            field_dict["not_after"] = not_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        edge_id = d.pop("edge_id")

        status = check_tcp_listener_tls_certificate_status_status(d.pop("status"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        _not_after = d.pop("not_after", UNSET)
        not_after: datetime.datetime | Unset
        if isinstance(_not_after, Unset):
            not_after = UNSET
        else:
            not_after = datetime.datetime.fromisoformat(_not_after)

        tcp_listener_tls_certificate_status = cls(
            edge_id=edge_id,
            status=status,
            observed_at=observed_at,
            not_after=not_after,
        )

        return tcp_listener_tls_certificate_status
