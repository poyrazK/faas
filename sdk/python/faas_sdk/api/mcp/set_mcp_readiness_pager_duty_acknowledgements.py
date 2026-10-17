from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_readiness_pager_duty_acknowledgement_policy import MCPReadinessPagerDutyAcknowledgementPolicy
from ...models.set_mcp_readiness_pager_duty_acknowledgements_request import (
    SetMCPReadinessPagerDutyAcknowledgementsRequest,
)
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: SetMCPReadinessPagerDutyAcknowledgementsRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/alerts/{id}/mcp-readiness-pagerduty-acknowledgements".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MCPReadinessPagerDutyAcknowledgementPolicy | None:
    if response.status_code == 200:
        response_200 = MCPReadinessPagerDutyAcknowledgementPolicy.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | MCPReadinessPagerDutyAcknowledgementPolicy]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: SetMCPReadinessPagerDutyAcknowledgementsRequest,
) -> Response[Any | MCPReadinessPagerDutyAcknowledgementPolicy]:
    """Configure signed PagerDuty acknowledgement sync

     Admin scope and MFA required. Enable with the selected primary or fallback native PagerDuty version,
    service ID and write-only v3 subscription signing secret. Destination defaults to primary; fallback
    requires expected_fallback_version instead of expected_pagerduty_version and matches only incidents
    with activated fallback. The server binds the native receiver fingerprint and region. Only the
    supplied current signing secret is accepted; use a fresh secret to rotate credentials. Each
    destination has independent secrets and policy versions. Saving one leaves the other unchanged.
    Disabling clears only the selected destination credentials immediately; when destination is omitted
    on disable, the original callback binding is selected. Signed acknowledgements stop reminders only
    for a matching pinned incident. Optional sync_unacknowledgements accepts signed
    incident.unacknowledged events. Reminders resume within the existing interval and budget only after
    every tracked provider acknowledgement clears. Existing untracked or manual acknowledgements require
    local release. Monitoring and recovery remain driven by readiness probes.

    Args:
        slug (str):
        id (UUID):
        body (SetMCPReadinessPagerDutyAcknowledgementsRequest): Version-fenced replacement of a
            PagerDuty acknowledgement webhook binding and write-only signing secret.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessPagerDutyAcknowledgementPolicy]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: SetMCPReadinessPagerDutyAcknowledgementsRequest,
) -> Any | MCPReadinessPagerDutyAcknowledgementPolicy | None:
    """Configure signed PagerDuty acknowledgement sync

     Admin scope and MFA required. Enable with the selected primary or fallback native PagerDuty version,
    service ID and write-only v3 subscription signing secret. Destination defaults to primary; fallback
    requires expected_fallback_version instead of expected_pagerduty_version and matches only incidents
    with activated fallback. The server binds the native receiver fingerprint and region. Only the
    supplied current signing secret is accepted; use a fresh secret to rotate credentials. Each
    destination has independent secrets and policy versions. Saving one leaves the other unchanged.
    Disabling clears only the selected destination credentials immediately; when destination is omitted
    on disable, the original callback binding is selected. Signed acknowledgements stop reminders only
    for a matching pinned incident. Optional sync_unacknowledgements accepts signed
    incident.unacknowledged events. Reminders resume within the existing interval and budget only after
    every tracked provider acknowledgement clears. Existing untracked or manual acknowledgements require
    local release. Monitoring and recovery remain driven by readiness probes.

    Args:
        slug (str):
        id (UUID):
        body (SetMCPReadinessPagerDutyAcknowledgementsRequest): Version-fenced replacement of a
            PagerDuty acknowledgement webhook binding and write-only signing secret.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessPagerDutyAcknowledgementPolicy
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: SetMCPReadinessPagerDutyAcknowledgementsRequest,
) -> Response[Any | MCPReadinessPagerDutyAcknowledgementPolicy]:
    """Configure signed PagerDuty acknowledgement sync

     Admin scope and MFA required. Enable with the selected primary or fallback native PagerDuty version,
    service ID and write-only v3 subscription signing secret. Destination defaults to primary; fallback
    requires expected_fallback_version instead of expected_pagerduty_version and matches only incidents
    with activated fallback. The server binds the native receiver fingerprint and region. Only the
    supplied current signing secret is accepted; use a fresh secret to rotate credentials. Each
    destination has independent secrets and policy versions. Saving one leaves the other unchanged.
    Disabling clears only the selected destination credentials immediately; when destination is omitted
    on disable, the original callback binding is selected. Signed acknowledgements stop reminders only
    for a matching pinned incident. Optional sync_unacknowledgements accepts signed
    incident.unacknowledged events. Reminders resume within the existing interval and budget only after
    every tracked provider acknowledgement clears. Existing untracked or manual acknowledgements require
    local release. Monitoring and recovery remain driven by readiness probes.

    Args:
        slug (str):
        id (UUID):
        body (SetMCPReadinessPagerDutyAcknowledgementsRequest): Version-fenced replacement of a
            PagerDuty acknowledgement webhook binding and write-only signing secret.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessPagerDutyAcknowledgementPolicy]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: SetMCPReadinessPagerDutyAcknowledgementsRequest,
) -> Any | MCPReadinessPagerDutyAcknowledgementPolicy | None:
    """Configure signed PagerDuty acknowledgement sync

     Admin scope and MFA required. Enable with the selected primary or fallback native PagerDuty version,
    service ID and write-only v3 subscription signing secret. Destination defaults to primary; fallback
    requires expected_fallback_version instead of expected_pagerduty_version and matches only incidents
    with activated fallback. The server binds the native receiver fingerprint and region. Only the
    supplied current signing secret is accepted; use a fresh secret to rotate credentials. Each
    destination has independent secrets and policy versions. Saving one leaves the other unchanged.
    Disabling clears only the selected destination credentials immediately; when destination is omitted
    on disable, the original callback binding is selected. Signed acknowledgements stop reminders only
    for a matching pinned incident. Optional sync_unacknowledgements accepts signed
    incident.unacknowledged events. Reminders resume within the existing interval and budget only after
    every tracked provider acknowledgement clears. Existing untracked or manual acknowledgements require
    local release. Monitoring and recovery remain driven by readiness probes.

    Args:
        slug (str):
        id (UUID):
        body (SetMCPReadinessPagerDutyAcknowledgementsRequest): Version-fenced replacement of a
            PagerDuty acknowledgement webhook binding and write-only signing secret.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessPagerDutyAcknowledgementPolicy
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
