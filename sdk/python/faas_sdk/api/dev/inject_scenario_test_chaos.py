from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.inject_scenario_test_chaos_request import InjectScenarioTestChaosRequest
from ...models.inject_scenario_test_chaos_response import InjectScenarioTestChaosResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    run_id: str,
    *,
    body: InjectScenarioTestChaosRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/dev/test-runs/{run_id}/chaos".format(
            run_id=quote(str(run_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> InjectScenarioTestChaosResponse | Problem | None:
    if response.status_code == 200:
        response_200 = InjectScenarioTestChaosResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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
) -> Response[InjectScenarioTestChaosResponse | Problem]:
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
    body: InjectScenarioTestChaosRequest,
) -> Response[InjectScenarioTestChaosResponse | Problem]:
    """Install a bounded fault plan for an isolated scenario run.

     Rules apply only to authenticated internal service calls between registered members, expire
    automatically, and cannot affect production or public traffic.

    Args:
        run_id (str):
        body (InjectScenarioTestChaosRequest): Bounded fault plan applied only to service calls
            within one scenario test namespace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InjectScenarioTestChaosResponse | Problem]
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
    body: InjectScenarioTestChaosRequest,
) -> InjectScenarioTestChaosResponse | Problem | None:
    """Install a bounded fault plan for an isolated scenario run.

     Rules apply only to authenticated internal service calls between registered members, expire
    automatically, and cannot affect production or public traffic.

    Args:
        run_id (str):
        body (InjectScenarioTestChaosRequest): Bounded fault plan applied only to service calls
            within one scenario test namespace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InjectScenarioTestChaosResponse | Problem
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
    body: InjectScenarioTestChaosRequest,
) -> Response[InjectScenarioTestChaosResponse | Problem]:
    """Install a bounded fault plan for an isolated scenario run.

     Rules apply only to authenticated internal service calls between registered members, expire
    automatically, and cannot affect production or public traffic.

    Args:
        run_id (str):
        body (InjectScenarioTestChaosRequest): Bounded fault plan applied only to service calls
            within one scenario test namespace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InjectScenarioTestChaosResponse | Problem]
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
    body: InjectScenarioTestChaosRequest,
) -> InjectScenarioTestChaosResponse | Problem | None:
    """Install a bounded fault plan for an isolated scenario run.

     Rules apply only to authenticated internal service calls between registered members, expire
    automatically, and cannot affect production or public traffic.

    Args:
        run_id (str):
        body (InjectScenarioTestChaosRequest): Bounded fault plan applied only to service calls
            within one scenario test namespace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InjectScenarioTestChaosResponse | Problem
    """

    return (
        await asyncio_detailed(
            run_id=run_id,
            client=client,
            body=body,
        )
    ).parsed
