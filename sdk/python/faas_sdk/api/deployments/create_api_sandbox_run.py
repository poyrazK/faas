from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_run_report import APISandboxRunReport
from ...models.api_sandbox_run_request import APISandboxRunRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: UUID,
    run: UUID,
    *,
    body: APISandboxRunRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/deployments/{deployment}/api-sandbox-runs/{run}".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
            run=quote(str(run), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxRunReport | Problem | None:
    if response.status_code == 200:
        response_200 = APISandboxRunReport.from_dict(response.json())

        return response_200

    if response.status_code == 202:
        response_202 = APISandboxRunReport.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxRunReport | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxRunRequest,
) -> Response[APISandboxRunReport | Problem]:
    """Start a scenario run in a fresh copy of the selected release.

     Repeat with the same run UUID, scenario revision, and mode to recover the existing run. A partner
    run waits for the partner's ordered HTTP requests, then runs the final GET with its JSON assertions.

    Args:
        slug (str):
        deployment (UUID):
        run (UUID):
        body (APISandboxRunRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxRunReport | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        run=run,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxRunRequest,
) -> APISandboxRunReport | Problem | None:
    """Start a scenario run in a fresh copy of the selected release.

     Repeat with the same run UUID, scenario revision, and mode to recover the existing run. A partner
    run waits for the partner's ordered HTTP requests, then runs the final GET with its JSON assertions.

    Args:
        slug (str):
        deployment (UUID):
        run (UUID):
        body (APISandboxRunRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxRunReport | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        run=run,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxRunRequest,
) -> Response[APISandboxRunReport | Problem]:
    """Start a scenario run in a fresh copy of the selected release.

     Repeat with the same run UUID, scenario revision, and mode to recover the existing run. A partner
    run waits for the partner's ordered HTTP requests, then runs the final GET with its JSON assertions.

    Args:
        slug (str):
        deployment (UUID):
        run (UUID):
        body (APISandboxRunRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxRunReport | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        run=run,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxRunRequest,
) -> APISandboxRunReport | Problem | None:
    """Start a scenario run in a fresh copy of the selected release.

     Repeat with the same run UUID, scenario revision, and mode to recover the existing run. A partner
    run waits for the partner's ordered HTTP requests, then runs the final GET with its JSON assertions.

    Args:
        slug (str):
        deployment (UUID):
        run (UUID):
        body (APISandboxRunRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxRunReport | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            run=run,
            client=client,
            body=body,
        )
    ).parsed
