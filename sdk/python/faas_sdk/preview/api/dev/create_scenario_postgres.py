from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_scenario_postgres_request import CreateScenarioPostgresRequest
from ...models.problem import Problem
from ...models.scenario_postgres_receipt import ScenarioPostgresReceipt
from ...types import Response


def _get_kwargs(
    run_id: str,
    *,
    body: CreateScenarioPostgresRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/dev/test-runs/{run_id}/postgres".format(
            run_id=quote(str(run_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ScenarioPostgresReceipt | None:
    if response.status_code == 201:
        response_201 = ScenarioPostgresReceipt.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ScenarioPostgresReceipt]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    run_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateScenarioPostgresRequest,
) -> Response[Problem | ScenarioPostgresReceipt]:
    """Provision a disposable PostgreSQL fixture before deploying a rehearsal workload.

     Opt-in operator preview for contained warm rehearsals. Executes declared SQL as an unprivileged
    login on a dedicated fixture cluster, independently captures selected starting rows, and injects
    DATABASE_URL as a sealed ephemeral app secret. Connection URLs and passwords are never returned. One
    fresh database per account/run; no existing deployment, managed binding, or production snapshot is
    accepted.

    Args:
        run_id (str):
        body (CreateScenarioPostgresRequest): Sanitized fixture SQL and its exact collection
            contract for a fresh contained workload.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScenarioPostgresReceipt]
    """

    kwargs = _get_kwargs(
        run_id=run_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    run_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateScenarioPostgresRequest,
) -> Problem | ScenarioPostgresReceipt | None:
    """Provision a disposable PostgreSQL fixture before deploying a rehearsal workload.

     Opt-in operator preview for contained warm rehearsals. Executes declared SQL as an unprivileged
    login on a dedicated fixture cluster, independently captures selected starting rows, and injects
    DATABASE_URL as a sealed ephemeral app secret. Connection URLs and passwords are never returned. One
    fresh database per account/run; no existing deployment, managed binding, or production snapshot is
    accepted.

    Args:
        run_id (str):
        body (CreateScenarioPostgresRequest): Sanitized fixture SQL and its exact collection
            contract for a fresh contained workload.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScenarioPostgresReceipt
    """

    return sync_detailed(
        run_id=run_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    run_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateScenarioPostgresRequest,
) -> Response[Problem | ScenarioPostgresReceipt]:
    """Provision a disposable PostgreSQL fixture before deploying a rehearsal workload.

     Opt-in operator preview for contained warm rehearsals. Executes declared SQL as an unprivileged
    login on a dedicated fixture cluster, independently captures selected starting rows, and injects
    DATABASE_URL as a sealed ephemeral app secret. Connection URLs and passwords are never returned. One
    fresh database per account/run; no existing deployment, managed binding, or production snapshot is
    accepted.

    Args:
        run_id (str):
        body (CreateScenarioPostgresRequest): Sanitized fixture SQL and its exact collection
            contract for a fresh contained workload.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScenarioPostgresReceipt]
    """

    kwargs = _get_kwargs(
        run_id=run_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    run_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateScenarioPostgresRequest,
) -> Problem | ScenarioPostgresReceipt | None:
    """Provision a disposable PostgreSQL fixture before deploying a rehearsal workload.

     Opt-in operator preview for contained warm rehearsals. Executes declared SQL as an unprivileged
    login on a dedicated fixture cluster, independently captures selected starting rows, and injects
    DATABASE_URL as a sealed ephemeral app secret. Connection URLs and passwords are never returned. One
    fresh database per account/run; no existing deployment, managed binding, or production snapshot is
    accepted.

    Args:
        run_id (str):
        body (CreateScenarioPostgresRequest): Sanitized fixture SQL and its exact collection
            contract for a fresh contained workload.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScenarioPostgresReceipt
    """

    return (
        await asyncio_detailed(
            run_id=run_id,
            client=client,
            body=body,
        )
    ).parsed
