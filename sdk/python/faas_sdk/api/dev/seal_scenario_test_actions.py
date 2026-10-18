from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.scenario_action_snapshot import ScenarioActionSnapshot
from ...types import Response


def _get_kwargs(
    run_id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/dev/test-runs/{run_id}/actions/seal".format(
            run_id=quote(str(run_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ScenarioActionSnapshot | None:
    if response.status_code == 200:
        response_200 = ScenarioActionSnapshot.from_dict(response.json())

        return response_200

    if response.status_code == 202:
        response_202 = ScenarioActionSnapshot.from_dict(response.json())

        return response_202

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ScenarioActionSnapshot]:
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
) -> Response[Problem | ScenarioActionSnapshot]:
    """Close a test run and export its supported action evidence.

     Waits for supported managed work to settle, then irreversibly fences new simulation relay admission
    and waits for owning recorders to acknowledge closing. Previously admitted requests may finish.
    Repeating a successful seal returns the immutable retained snapshot. Coverage includes managed
    webhook intent and enrolled simulated relay attempts; arbitrary direct network traffic and external
    queues remain outside scope.

    Args:
        run_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScenarioActionSnapshot]
    """

    kwargs = _get_kwargs(
        run_id=run_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    run_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ScenarioActionSnapshot | None:
    """Close a test run and export its supported action evidence.

     Waits for supported managed work to settle, then irreversibly fences new simulation relay admission
    and waits for owning recorders to acknowledge closing. Previously admitted requests may finish.
    Repeating a successful seal returns the immutable retained snapshot. Coverage includes managed
    webhook intent and enrolled simulated relay attempts; arbitrary direct network traffic and external
    queues remain outside scope.

    Args:
        run_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScenarioActionSnapshot
    """

    return sync_detailed(
        run_id=run_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    run_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ScenarioActionSnapshot]:
    """Close a test run and export its supported action evidence.

     Waits for supported managed work to settle, then irreversibly fences new simulation relay admission
    and waits for owning recorders to acknowledge closing. Previously admitted requests may finish.
    Repeating a successful seal returns the immutable retained snapshot. Coverage includes managed
    webhook intent and enrolled simulated relay attempts; arbitrary direct network traffic and external
    queues remain outside scope.

    Args:
        run_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScenarioActionSnapshot]
    """

    kwargs = _get_kwargs(
        run_id=run_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    run_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ScenarioActionSnapshot | None:
    """Close a test run and export its supported action evidence.

     Waits for supported managed work to settle, then irreversibly fences new simulation relay admission
    and waits for owning recorders to acknowledge closing. Previously admitted requests may finish.
    Repeating a successful seal returns the immutable retained snapshot. Coverage includes managed
    webhook intent and enrolled simulated relay attempts; arbitrary direct network traffic and external
    queues remain outside scope.

    Args:
        run_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScenarioActionSnapshot
    """

    return (
        await asyncio_detailed(
            run_id=run_id,
            client=client,
        )
    ).parsed
