from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.workflow_outbound_spec_method import WorkflowOutboundSpecMethod, check_workflow_outbound_spec_method
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowOutboundSpec")


@_attrs_define
class WorkflowOutboundSpec:
    """Call an existing customer managed outbound integration bound to this app.
    Credentials and fixed-origin routing remain with outboundd. Input is a
    templated JSON body; GET and HEAD have no body and forbid explicit input.
    One provider call occurs per workflow attempt. Automatic mutating retries
    require explicit provider idempotency support. Workflow outputs contain
    status and body; sensitive headers and failed-response bodies are omitted.

    """

    integration_id: UUID
    """Canonical UUID of the customer managed integration bound to the app."""
    method: WorkflowOutboundSpecMethod
    """Provider HTTP method within both integration and app binding permissions."""
    path: str
    """Fixed canonical relative provider path; no URL, query, escaping, or traversal."""
    idempotency_supported: bool | Unset = False
    """Assert that the provider deduplicates the stable Idempotency-Key for this mutating operation; enables
    retries and recovery."""

    def to_dict(self) -> dict[str, Any]:
        integration_id = str(self.integration_id)

        method: str = self.method

        path = self.path

        idempotency_supported = self.idempotency_supported

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "integration_id": integration_id,
                "method": method,
                "path": path,
            }
        )
        if idempotency_supported is not UNSET:
            field_dict["idempotency_supported"] = idempotency_supported

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        integration_id = UUID(d.pop("integration_id"))

        method = check_workflow_outbound_spec_method(d.pop("method"))

        path = d.pop("path")

        idempotency_supported = d.pop("idempotency_supported", UNSET)

        workflow_outbound_spec = cls(
            integration_id=integration_id,
            method=method,
            path=path,
            idempotency_supported=idempotency_supported,
        )

        return workflow_outbound_spec
