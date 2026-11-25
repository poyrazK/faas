from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_readiness_pager_duty_policy import MCPReadinessPagerDutyPolicy
from ...models.set_mcp_readiness_pager_duty_request import SetMCPReadinessPagerDutyRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: SetMCPReadinessPagerDutyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/alerts/{id}/mcp-readiness-pagerduty".format(
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
) -> Any | MCPReadinessPagerDutyPolicy | None:
    if response.status_code == 200:
        response_200 = MCPReadinessPagerDutyPolicy.from_dict(response.json())

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
) -> Response[Any | MCPReadinessPagerDutyPolicy]:
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
    body: SetMCPReadinessPagerDutyRequest,
) -> Response[Any | MCPReadinessPagerDutyPolicy]:
    """Configure native PagerDuty primary delivery

     Admin scope and MFA required. Enable using a write-only 32-character Events API v2 integration key
    for the selected US or EU endpoint (US by default). Configuration is pinned at an incident's first
    delivery attempt; changes and disabling affect unattempted incidents. Existing pinned receivers
    continue receiving recovery. Webhook and distinct native PagerDuty fallback remain available. Keys
    are encrypted with a dedicated namespace. Upgrade all readiness evaluators before enabling.
    PagerDuty acceptance does not confirm paging. A separate signed acknowledgement webhook policy can
    synchronize operator acknowledgement.

    Args:
        slug (str):
        id (UUID):
        body (SetMCPReadinessPagerDutyRequest): Version-fenced PagerDuty primary receiver
            configuration with write-only credentials.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessPagerDutyPolicy]
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
    body: SetMCPReadinessPagerDutyRequest,
) -> Any | MCPReadinessPagerDutyPolicy | None:
    """Configure native PagerDuty primary delivery

     Admin scope and MFA required. Enable using a write-only 32-character Events API v2 integration key
    for the selected US or EU endpoint (US by default). Configuration is pinned at an incident's first
    delivery attempt; changes and disabling affect unattempted incidents. Existing pinned receivers
    continue receiving recovery. Webhook and distinct native PagerDuty fallback remain available. Keys
    are encrypted with a dedicated namespace. Upgrade all readiness evaluators before enabling.
    PagerDuty acceptance does not confirm paging. A separate signed acknowledgement webhook policy can
    synchronize operator acknowledgement.

    Args:
        slug (str):
        id (UUID):
        body (SetMCPReadinessPagerDutyRequest): Version-fenced PagerDuty primary receiver
            configuration with write-only credentials.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessPagerDutyPolicy
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
    body: SetMCPReadinessPagerDutyRequest,
) -> Response[Any | MCPReadinessPagerDutyPolicy]:
    """Configure native PagerDuty primary delivery

     Admin scope and MFA required. Enable using a write-only 32-character Events API v2 integration key
    for the selected US or EU endpoint (US by default). Configuration is pinned at an incident's first
    delivery attempt; changes and disabling affect unattempted incidents. Existing pinned receivers
    continue receiving recovery. Webhook and distinct native PagerDuty fallback remain available. Keys
    are encrypted with a dedicated namespace. Upgrade all readiness evaluators before enabling.
    PagerDuty acceptance does not confirm paging. A separate signed acknowledgement webhook policy can
    synchronize operator acknowledgement.

    Args:
        slug (str):
        id (UUID):
        body (SetMCPReadinessPagerDutyRequest): Version-fenced PagerDuty primary receiver
            configuration with write-only credentials.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessPagerDutyPolicy]
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
    body: SetMCPReadinessPagerDutyRequest,
) -> Any | MCPReadinessPagerDutyPolicy | None:
    """Configure native PagerDuty primary delivery

     Admin scope and MFA required. Enable using a write-only 32-character Events API v2 integration key
    for the selected US or EU endpoint (US by default). Configuration is pinned at an incident's first
    delivery attempt; changes and disabling affect unattempted incidents. Existing pinned receivers
    continue receiving recovery. Webhook and distinct native PagerDuty fallback remain available. Keys
    are encrypted with a dedicated namespace. Upgrade all readiness evaluators before enabling.
    PagerDuty acceptance does not confirm paging. A separate signed acknowledgement webhook policy can
    synchronize operator acknowledgement.

    Args:
        slug (str):
        id (UUID):
        body (SetMCPReadinessPagerDutyRequest): Version-fenced PagerDuty primary receiver
            configuration with write-only credentials.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessPagerDutyPolicy
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
