from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_open_api_policy_preview_response import AppOpenAPIPolicyPreviewResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/openapi/preview".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppOpenAPIPolicyPreviewResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppOpenAPIPolicyPreviewResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppOpenAPIPolicyPreviewResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppOpenAPIPolicyPreviewResponse | Problem]:
    """Preview declared routes, observed routes, and matching edge policies.

     Read-only route-policy preview for API-hosting roadmap item 11.
    Joins the persisted OpenAPI declaration with gatewayd's observed
    route labels and the app's edge rules. Each route is classified as
    `matched`, `declared_only`, or `observed_only`; `covered` is true
    when at least one enabled edge rule matches the path and method.
    When the gateway bridge is unavailable the response remains useful,
    sets `observed_available` to false, and reports
    `source=degraded: routes_unavailable`. No policy or document writes
    occur on this endpoint.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppOpenAPIPolicyPreviewResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> AppOpenAPIPolicyPreviewResponse | Problem | None:
    """Preview declared routes, observed routes, and matching edge policies.

     Read-only route-policy preview for API-hosting roadmap item 11.
    Joins the persisted OpenAPI declaration with gatewayd's observed
    route labels and the app's edge rules. Each route is classified as
    `matched`, `declared_only`, or `observed_only`; `covered` is true
    when at least one enabled edge rule matches the path and method.
    When the gateway bridge is unavailable the response remains useful,
    sets `observed_available` to false, and reports
    `source=degraded: routes_unavailable`. No policy or document writes
    occur on this endpoint.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppOpenAPIPolicyPreviewResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppOpenAPIPolicyPreviewResponse | Problem]:
    """Preview declared routes, observed routes, and matching edge policies.

     Read-only route-policy preview for API-hosting roadmap item 11.
    Joins the persisted OpenAPI declaration with gatewayd's observed
    route labels and the app's edge rules. Each route is classified as
    `matched`, `declared_only`, or `observed_only`; `covered` is true
    when at least one enabled edge rule matches the path and method.
    When the gateway bridge is unavailable the response remains useful,
    sets `observed_available` to false, and reports
    `source=degraded: routes_unavailable`. No policy or document writes
    occur on this endpoint.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppOpenAPIPolicyPreviewResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> AppOpenAPIPolicyPreviewResponse | Problem | None:
    """Preview declared routes, observed routes, and matching edge policies.

     Read-only route-policy preview for API-hosting roadmap item 11.
    Joins the persisted OpenAPI declaration with gatewayd's observed
    route labels and the app's edge rules. Each route is classified as
    `matched`, `declared_only`, or `observed_only`; `covered` is true
    when at least one enabled edge rule matches the path and method.
    When the gateway bridge is unavailable the response remains useful,
    sets `observed_available` to false, and reports
    `source=degraded: routes_unavailable`. No policy or document writes
    occur on this endpoint.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppOpenAPIPolicyPreviewResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
