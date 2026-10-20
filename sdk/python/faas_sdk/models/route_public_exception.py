from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.route_public_exception_method import RoutePublicExceptionMethod, check_route_public_exception_method

T = TypeVar("T", bound="RoutePublicException")


@_attrs_define
class RoutePublicException:
    """Exact captured operation exempt from group checks. Rationale is replaced with declared_public_exception in
    normalized plans; exceptions do not change runtime access.

    """

    method: RoutePublicExceptionMethod
    path: str
    reason: str

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        path = self.path

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "method": method,
                "path": path,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        method = check_route_public_exception_method(d.pop("method"))

        path = d.pop("path")

        reason = d.pop("reason")

        route_public_exception = cls(
            method=method,
            path=path,
            reason=reason,
        )

        return route_public_exception
