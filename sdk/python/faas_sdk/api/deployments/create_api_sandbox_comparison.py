from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_comparison_report import APISandboxComparisonReport
from ...models.api_sandbox_comparison_request import APISandboxComparisonRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: UUID,
    comparison: UUID,
    *,
    body: APISandboxComparisonRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/deployments/{deployment}/api-sandbox-comparisons/{comparison}".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
            comparison=quote(str(comparison), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxComparisonReport | Problem:
    if response.status_code == 200:
        response_200 = APISandboxComparisonReport.from_dict(response.json())

        return response_200

    if response.status_code == 202:
        response_202 = APISandboxComparisonReport.from_dict(response.json())

        return response_202

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxComparisonReport | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    comparison: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxComparisonRequest,
) -> Response[APISandboxComparisonReport | Problem]:
    """Run the same saved sandbox scenario on two retained releases.

     The candidate comes from the deployment path. Choose a different retained baseline from the same
    application and use a current app library or candidate scenario revision. Reuse the comparison UUID
    with the same inputs to retry.

    Args:
        slug (str):
        deployment (UUID):
        comparison (UUID):
        body (APISandboxComparisonRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxComparisonReport | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        comparison=comparison,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    comparison: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxComparisonRequest,
) -> APISandboxComparisonReport | Problem | None:
    """Run the same saved sandbox scenario on two retained releases.

     The candidate comes from the deployment path. Choose a different retained baseline from the same
    application and use a current app library or candidate scenario revision. Reuse the comparison UUID
    with the same inputs to retry.

    Args:
        slug (str):
        deployment (UUID):
        comparison (UUID):
        body (APISandboxComparisonRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxComparisonReport | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        comparison=comparison,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    comparison: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxComparisonRequest,
) -> Response[APISandboxComparisonReport | Problem]:
    """Run the same saved sandbox scenario on two retained releases.

     The candidate comes from the deployment path. Choose a different retained baseline from the same
    application and use a current app library or candidate scenario revision. Reuse the comparison UUID
    with the same inputs to retry.

    Args:
        slug (str):
        deployment (UUID):
        comparison (UUID):
        body (APISandboxComparisonRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxComparisonReport | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        comparison=comparison,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    comparison: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxComparisonRequest,
) -> APISandboxComparisonReport | Problem | None:
    """Run the same saved sandbox scenario on two retained releases.

     The candidate comes from the deployment path. Choose a different retained baseline from the same
    application and use a current app library or candidate scenario revision. Reuse the comparison UUID
    with the same inputs to retry.

    Args:
        slug (str):
        deployment (UUID):
        comparison (UUID):
        body (APISandboxComparisonRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxComparisonReport | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            comparison=comparison,
            client=client,
            body=body,
        )
    ).parsed
