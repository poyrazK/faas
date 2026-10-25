from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.clear_obsolete_deployments_body import ClearObsoleteDeploymentsBody
from ...models.clear_obsolete_report import ClearObsoleteReport
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: ClearObsoleteDeploymentsBody | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/deployments/clear-obsolete".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ClearObsoleteReport | Problem | None:
    if response.status_code == 200:
        response_200 = ClearObsoleteReport.from_dict(response.json())

        return response_200

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ClearObsoleteReport | Problem]:
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
    body: ClearObsoleteDeploymentsBody | Unset = UNSET,
) -> Response[ClearObsoleteReport | Problem]:
    """Bulk soft-delete terminal-but-not-current deployments.

     ADR-124 deployment queue controls — bulk soft-delete rows
    in {superseded, failed, cancelled} older than the cutoff
    (default 168h). Plan-gated (Free returns 402
    `plan_reorder_disabled`). Retention
    cap enforced inside the store so INV 3 stays satisfied.

    Args:
        slug (str):
        body (ClearObsoleteDeploymentsBody | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ClearObsoleteReport | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ClearObsoleteDeploymentsBody | Unset = UNSET,
) -> ClearObsoleteReport | Problem | None:
    """Bulk soft-delete terminal-but-not-current deployments.

     ADR-124 deployment queue controls — bulk soft-delete rows
    in {superseded, failed, cancelled} older than the cutoff
    (default 168h). Plan-gated (Free returns 402
    `plan_reorder_disabled`). Retention
    cap enforced inside the store so INV 3 stays satisfied.

    Args:
        slug (str):
        body (ClearObsoleteDeploymentsBody | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ClearObsoleteReport | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ClearObsoleteDeploymentsBody | Unset = UNSET,
) -> Response[ClearObsoleteReport | Problem]:
    """Bulk soft-delete terminal-but-not-current deployments.

     ADR-124 deployment queue controls — bulk soft-delete rows
    in {superseded, failed, cancelled} older than the cutoff
    (default 168h). Plan-gated (Free returns 402
    `plan_reorder_disabled`). Retention
    cap enforced inside the store so INV 3 stays satisfied.

    Args:
        slug (str):
        body (ClearObsoleteDeploymentsBody | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ClearObsoleteReport | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ClearObsoleteDeploymentsBody | Unset = UNSET,
) -> ClearObsoleteReport | Problem | None:
    """Bulk soft-delete terminal-but-not-current deployments.

     ADR-124 deployment queue controls — bulk soft-delete rows
    in {superseded, failed, cancelled} older than the cutoff
    (default 168h). Plan-gated (Free returns 402
    `plan_reorder_disabled`). Retention
    cap enforced inside the store so INV 3 stays satisfied.

    Args:
        slug (str):
        body (ClearObsoleteDeploymentsBody | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ClearObsoleteReport | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
