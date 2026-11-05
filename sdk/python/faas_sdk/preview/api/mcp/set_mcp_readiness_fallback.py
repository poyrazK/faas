from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_readiness_fallback_policy import MCPReadinessFallbackPolicy
from ...models.set_mcp_readiness_fallback_request import SetMCPReadinessFallbackRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: SetMCPReadinessFallbackRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/alerts/{id}/mcp-readiness-fallback".format(
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
) -> Any | MCPReadinessFallbackPolicy | None:
    if response.status_code == 200:
        response_200 = MCPReadinessFallbackPolicy.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | MCPReadinessFallbackPolicy]:
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
    body: SetMCPReadinessFallbackRequest,
) -> Response[Any | MCPReadinessFallbackPolicy]:
    """Replace a readiness fallback destination policy

     Admin scope and MFA required. expected_version fences concurrent edits. Enable selects webhook
    (default) with a distinct public HTTP(S) destination and signing secret, or PagerDuty with a
    separate integration key and optional US/EU region. Primary and fallback cannot select the same
    PagerDuty integration. Threshold counts completed failed primary dispatch claims per logical event.
    Changes apply to incidents not yet attempted; a new webhook secret for the same URL also supplies
    ongoing pinned deliveries. PagerDuty provider, region and integration identity stay pinned;
    credential re-encryption for the identical integration refreshes ongoing deliveries. Disabling
    affects new incidents; disable the alert rule to pause existing deliveries. Upgrade all evaluators
    before enabling fallback.

    Args:
        slug (str):
        id (UUID):
        body (SetMCPReadinessFallbackRequest): Optimistically versioned fallback replacement.
            Webhook requires webhook_url and webhook_secret. PagerDuty requires provider=pagerduty and
            integration_key with optional region. Enabled policies require after_failures; disabling
            omits all destination fields.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessFallbackPolicy]
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
    body: SetMCPReadinessFallbackRequest,
) -> Any | MCPReadinessFallbackPolicy | None:
    """Replace a readiness fallback destination policy

     Admin scope and MFA required. expected_version fences concurrent edits. Enable selects webhook
    (default) with a distinct public HTTP(S) destination and signing secret, or PagerDuty with a
    separate integration key and optional US/EU region. Primary and fallback cannot select the same
    PagerDuty integration. Threshold counts completed failed primary dispatch claims per logical event.
    Changes apply to incidents not yet attempted; a new webhook secret for the same URL also supplies
    ongoing pinned deliveries. PagerDuty provider, region and integration identity stay pinned;
    credential re-encryption for the identical integration refreshes ongoing deliveries. Disabling
    affects new incidents; disable the alert rule to pause existing deliveries. Upgrade all evaluators
    before enabling fallback.

    Args:
        slug (str):
        id (UUID):
        body (SetMCPReadinessFallbackRequest): Optimistically versioned fallback replacement.
            Webhook requires webhook_url and webhook_secret. PagerDuty requires provider=pagerduty and
            integration_key with optional region. Enabled policies require after_failures; disabling
            omits all destination fields.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessFallbackPolicy
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
    body: SetMCPReadinessFallbackRequest,
) -> Response[Any | MCPReadinessFallbackPolicy]:
    """Replace a readiness fallback destination policy

     Admin scope and MFA required. expected_version fences concurrent edits. Enable selects webhook
    (default) with a distinct public HTTP(S) destination and signing secret, or PagerDuty with a
    separate integration key and optional US/EU region. Primary and fallback cannot select the same
    PagerDuty integration. Threshold counts completed failed primary dispatch claims per logical event.
    Changes apply to incidents not yet attempted; a new webhook secret for the same URL also supplies
    ongoing pinned deliveries. PagerDuty provider, region and integration identity stay pinned;
    credential re-encryption for the identical integration refreshes ongoing deliveries. Disabling
    affects new incidents; disable the alert rule to pause existing deliveries. Upgrade all evaluators
    before enabling fallback.

    Args:
        slug (str):
        id (UUID):
        body (SetMCPReadinessFallbackRequest): Optimistically versioned fallback replacement.
            Webhook requires webhook_url and webhook_secret. PagerDuty requires provider=pagerduty and
            integration_key with optional region. Enabled policies require after_failures; disabling
            omits all destination fields.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessFallbackPolicy]
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
    body: SetMCPReadinessFallbackRequest,
) -> Any | MCPReadinessFallbackPolicy | None:
    """Replace a readiness fallback destination policy

     Admin scope and MFA required. expected_version fences concurrent edits. Enable selects webhook
    (default) with a distinct public HTTP(S) destination and signing secret, or PagerDuty with a
    separate integration key and optional US/EU region. Primary and fallback cannot select the same
    PagerDuty integration. Threshold counts completed failed primary dispatch claims per logical event.
    Changes apply to incidents not yet attempted; a new webhook secret for the same URL also supplies
    ongoing pinned deliveries. PagerDuty provider, region and integration identity stay pinned;
    credential re-encryption for the identical integration refreshes ongoing deliveries. Disabling
    affects new incidents; disable the alert rule to pause existing deliveries. Upgrade all evaluators
    before enabling fallback.

    Args:
        slug (str):
        id (UUID):
        body (SetMCPReadinessFallbackRequest): Optimistically versioned fallback replacement.
            Webhook requires webhook_url and webhook_secret. PagerDuty requires provider=pagerduty and
            integration_key with optional region. Enabled policies require after_failures; disabling
            omits all destination fields.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessFallbackPolicy
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
