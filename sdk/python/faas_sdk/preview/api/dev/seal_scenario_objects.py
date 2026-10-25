from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.scenario_objects_evidence import ScenarioObjectsEvidence
from ...models.seal_scenario_objects_request import SealScenarioObjectsRequest
from ...types import Response


def _get_kwargs(
    run_id: str,
    *,
    body: SealScenarioObjectsRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/dev/test-runs/{run_id}/objects/seal".format(
            run_id=quote(str(run_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ScenarioObjectsEvidence | None:
    if response.status_code == 200:
        response_200 = ScenarioObjectsEvidence.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ScenarioObjectsEvidence]:
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
    body: SealScenarioObjectsRequest,
) -> Response[Problem | ScenarioObjectsEvidence]:
    """Fence object requests, collect final content digests, and confirm cleanup.

     Requires a deployment belonging to the fixture app and complete sealed action evidence. New requests
    are fenced before waiting for admitted requests. Unsupported operations, unfinished uploads, bounds
    violations, collector errors or failed cleanup cannot produce complete evidence. Evidence
    publication and removal of object bodies share one transaction. Completed sealing is idempotent for
    the same deployment.

    Args:
        run_id (str):
        body (SealScenarioObjectsRequest): Deployment identity that must own this rehearsal
            object's primary app.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScenarioObjectsEvidence]
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
    body: SealScenarioObjectsRequest,
) -> Problem | ScenarioObjectsEvidence | None:
    """Fence object requests, collect final content digests, and confirm cleanup.

     Requires a deployment belonging to the fixture app and complete sealed action evidence. New requests
    are fenced before waiting for admitted requests. Unsupported operations, unfinished uploads, bounds
    violations, collector errors or failed cleanup cannot produce complete evidence. Evidence
    publication and removal of object bodies share one transaction. Completed sealing is idempotent for
    the same deployment.

    Args:
        run_id (str):
        body (SealScenarioObjectsRequest): Deployment identity that must own this rehearsal
            object's primary app.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScenarioObjectsEvidence
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
    body: SealScenarioObjectsRequest,
) -> Response[Problem | ScenarioObjectsEvidence]:
    """Fence object requests, collect final content digests, and confirm cleanup.

     Requires a deployment belonging to the fixture app and complete sealed action evidence. New requests
    are fenced before waiting for admitted requests. Unsupported operations, unfinished uploads, bounds
    violations, collector errors or failed cleanup cannot produce complete evidence. Evidence
    publication and removal of object bodies share one transaction. Completed sealing is idempotent for
    the same deployment.

    Args:
        run_id (str):
        body (SealScenarioObjectsRequest): Deployment identity that must own this rehearsal
            object's primary app.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScenarioObjectsEvidence]
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
    body: SealScenarioObjectsRequest,
) -> Problem | ScenarioObjectsEvidence | None:
    """Fence object requests, collect final content digests, and confirm cleanup.

     Requires a deployment belonging to the fixture app and complete sealed action evidence. New requests
    are fenced before waiting for admitted requests. Unsupported operations, unfinished uploads, bounds
    violations, collector errors or failed cleanup cannot produce complete evidence. Evidence
    publication and removal of object bodies share one transaction. Completed sealing is idempotent for
    the same deployment.

    Args:
        run_id (str):
        body (SealScenarioObjectsRequest): Deployment identity that must own this rehearsal
            object's primary app.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScenarioObjectsEvidence
    """

    return (
        await asyncio_detailed(
            run_id=run_id,
            client=client,
            body=body,
        )
    ).parsed
