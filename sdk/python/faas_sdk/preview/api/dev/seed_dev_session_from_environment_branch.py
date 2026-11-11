from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.seed_dev_session_from_environment_branch_request import SeedDevSessionFromEnvironmentBranchRequest
from ...models.seed_dev_session_from_environment_branch_response import SeedDevSessionFromEnvironmentBranchResponse
from ...types import Response


def _get_kwargs(
    project: str,
    *,
    body: SeedDevSessionFromEnvironmentBranchRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/dev/sessions/{project}/seed".format(
            project=quote(str(project), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SeedDevSessionFromEnvironmentBranchResponse | None:
    if response.status_code == 202:
        response_202 = SeedDevSessionFromEnvironmentBranchResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | SeedDevSessionFromEnvironmentBranchResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    project: str,
    *,
    client: AuthenticatedClient | Client,
    body: SeedDevSessionFromEnvironmentBranchRequest,
) -> Response[Problem | SeedDevSessionFromEnvironmentBranchResponse]:
    """Seed a rehearsal member from a simulated environment branch.

     Deploys one workload of a simulated environment branch into the developer session of a rehearsal
    run. The member reuses the branch deployment's build and gets the branch's variables and secrets;
    managed PostgreSQL and object-storage bindings are not copied, so the rehearsal declares isolated
    fixtures. The deployment goes live after one boot. Repeating the request returns the deployment
    already seeded.

    Args:
        project (str):
        body (SeedDevSessionFromEnvironmentBranchRequest): Deploys one workload of a simulated
            environment branch into a rehearsal member's developer session, reusing the branch's
            build.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SeedDevSessionFromEnvironmentBranchResponse]
    """

    kwargs = _get_kwargs(
        project=project,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    project: str,
    *,
    client: AuthenticatedClient | Client,
    body: SeedDevSessionFromEnvironmentBranchRequest,
) -> Problem | SeedDevSessionFromEnvironmentBranchResponse | None:
    """Seed a rehearsal member from a simulated environment branch.

     Deploys one workload of a simulated environment branch into the developer session of a rehearsal
    run. The member reuses the branch deployment's build and gets the branch's variables and secrets;
    managed PostgreSQL and object-storage bindings are not copied, so the rehearsal declares isolated
    fixtures. The deployment goes live after one boot. Repeating the request returns the deployment
    already seeded.

    Args:
        project (str):
        body (SeedDevSessionFromEnvironmentBranchRequest): Deploys one workload of a simulated
            environment branch into a rehearsal member's developer session, reusing the branch's
            build.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SeedDevSessionFromEnvironmentBranchResponse
    """

    return sync_detailed(
        project=project,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    project: str,
    *,
    client: AuthenticatedClient | Client,
    body: SeedDevSessionFromEnvironmentBranchRequest,
) -> Response[Problem | SeedDevSessionFromEnvironmentBranchResponse]:
    """Seed a rehearsal member from a simulated environment branch.

     Deploys one workload of a simulated environment branch into the developer session of a rehearsal
    run. The member reuses the branch deployment's build and gets the branch's variables and secrets;
    managed PostgreSQL and object-storage bindings are not copied, so the rehearsal declares isolated
    fixtures. The deployment goes live after one boot. Repeating the request returns the deployment
    already seeded.

    Args:
        project (str):
        body (SeedDevSessionFromEnvironmentBranchRequest): Deploys one workload of a simulated
            environment branch into a rehearsal member's developer session, reusing the branch's
            build.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SeedDevSessionFromEnvironmentBranchResponse]
    """

    kwargs = _get_kwargs(
        project=project,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    project: str,
    *,
    client: AuthenticatedClient | Client,
    body: SeedDevSessionFromEnvironmentBranchRequest,
) -> Problem | SeedDevSessionFromEnvironmentBranchResponse | None:
    """Seed a rehearsal member from a simulated environment branch.

     Deploys one workload of a simulated environment branch into the developer session of a rehearsal
    run. The member reuses the branch deployment's build and gets the branch's variables and secrets;
    managed PostgreSQL and object-storage bindings are not copied, so the rehearsal declares isolated
    fixtures. The deployment goes live after one boot. Repeating the request returns the deployment
    already seeded.

    Args:
        project (str):
        body (SeedDevSessionFromEnvironmentBranchRequest): Deploys one workload of a simulated
            environment branch into a rehearsal member's developer session, reusing the branch's
            build.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SeedDevSessionFromEnvironmentBranchResponse
    """

    return (
        await asyncio_detailed(
            project=project,
            client=client,
            body=body,
        )
    ).parsed
