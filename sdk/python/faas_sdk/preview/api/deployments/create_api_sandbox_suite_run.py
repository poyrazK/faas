from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_suite_run_report import APISandboxSuiteRunReport
from ...models.api_sandbox_suite_run_request import APISandboxSuiteRunRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    run: UUID,
    *,
    body: APISandboxSuiteRunRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/deployments/{id}/approvals/sandbox-suite-runs/{run}".format(
            id=quote(str(id), safe=""),
            run=quote(str(run), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxSuiteRunReport | Problem | None:
    if response.status_code == 200:
        response_200 = APISandboxSuiteRunReport.from_dict(response.json())

        return response_200

    if response.status_code == 202:
        response_202 = APISandboxSuiteRunReport.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxSuiteRunReport | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxSuiteRunRequest,
) -> Response[APISandboxSuiteRunReport | Problem]:
    """Queue the app's regression suite for a held build and live baseline.

     Pins the deployment artifacts and current library scenario revisions. Each scenario runs in a fresh
    pair of sandboxes. Reuse the run UUID and the same inputs to retry.

    Args:
        id (UUID):
        run (UUID):
        body (APISandboxSuiteRunRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxSuiteRunReport | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        run=run,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxSuiteRunRequest,
) -> APISandboxSuiteRunReport | Problem | None:
    """Queue the app's regression suite for a held build and live baseline.

     Pins the deployment artifacts and current library scenario revisions. Each scenario runs in a fresh
    pair of sandboxes. Reuse the run UUID and the same inputs to retry.

    Args:
        id (UUID):
        run (UUID):
        body (APISandboxSuiteRunRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxSuiteRunReport | Problem
    """

    return sync_detailed(
        id=id,
        run=run,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxSuiteRunRequest,
) -> Response[APISandboxSuiteRunReport | Problem]:
    """Queue the app's regression suite for a held build and live baseline.

     Pins the deployment artifacts and current library scenario revisions. Each scenario runs in a fresh
    pair of sandboxes. Reuse the run UUID and the same inputs to retry.

    Args:
        id (UUID):
        run (UUID):
        body (APISandboxSuiteRunRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxSuiteRunReport | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        run=run,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxSuiteRunRequest,
) -> APISandboxSuiteRunReport | Problem | None:
    """Queue the app's regression suite for a held build and live baseline.

     Pins the deployment artifacts and current library scenario revisions. Each scenario runs in a fresh
    pair of sandboxes. Reuse the run UUID and the same inputs to retry.

    Args:
        id (UUID):
        run (UUID):
        body (APISandboxSuiteRunRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxSuiteRunReport | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            run=run,
            client=client,
            body=body,
        )
    ).parsed
