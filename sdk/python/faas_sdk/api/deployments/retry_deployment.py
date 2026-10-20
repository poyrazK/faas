from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_response import DeploymentResponse
from ...models.problem import Problem
from ...models.retry_deployment_request import RetryDeploymentRequest
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: RetryDeploymentRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/deployments/{id}/retry".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentResponse | Problem | None:
    if response.status_code == 202:
        response_202 = DeploymentResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DeploymentResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RetryDeploymentRequest,
) -> Response[DeploymentResponse | Problem]:
    """Retry a failed deployment from a named stage (ADR-117 production-ready follow-on C2).

     Closes the production-ready gap exposed by ADR-117 §C4: a
    deployment that fails partway is restorable via
    `POST /v1/deployments/{id}/retry` with a `from_stage` field.
    The deployment row is duplicated (NOT mutated); the new
    row carries a fresh `stage_state.current` and a fresh
    `stage_state.history` so the dashboard's stage-progression
    timeline (and the CLI's `gregale deploys show <id>` summary)
    reflects the retry as a separate event.

    The closed-6 vocabulary mirrors `state.AllStageNames`
    (ADR-117); the API rejects unknown values with 400.
    Empty strings are rejected for the same reason.

    Auth chain mirrors `POST /v1/apps/{slug}/deployments`:
    `authLimited → requireMFA → requireScope(ScopesDeployWriteSurface)`.
    Returns 202 Accepted with the new deployment row (same
    shape as `POST /v1/apps/{slug}/deployments`).

    Args:
        id (str):
        body (RetryDeploymentRequest): Body for POST /v1/deployments/{id}/retry. Identifies the
            stage the retry should resume from. The closed-6 vocabulary mirrors `state.AllStageNames`
            (ADR-117); the API rejects unknown values with 400. Empty strings are rejected for the
            same reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RetryDeploymentRequest,
) -> DeploymentResponse | Problem | None:
    """Retry a failed deployment from a named stage (ADR-117 production-ready follow-on C2).

     Closes the production-ready gap exposed by ADR-117 §C4: a
    deployment that fails partway is restorable via
    `POST /v1/deployments/{id}/retry` with a `from_stage` field.
    The deployment row is duplicated (NOT mutated); the new
    row carries a fresh `stage_state.current` and a fresh
    `stage_state.history` so the dashboard's stage-progression
    timeline (and the CLI's `gregale deploys show <id>` summary)
    reflects the retry as a separate event.

    The closed-6 vocabulary mirrors `state.AllStageNames`
    (ADR-117); the API rejects unknown values with 400.
    Empty strings are rejected for the same reason.

    Auth chain mirrors `POST /v1/apps/{slug}/deployments`:
    `authLimited → requireMFA → requireScope(ScopesDeployWriteSurface)`.
    Returns 202 Accepted with the new deployment row (same
    shape as `POST /v1/apps/{slug}/deployments`).

    Args:
        id (str):
        body (RetryDeploymentRequest): Body for POST /v1/deployments/{id}/retry. Identifies the
            stage the retry should resume from. The closed-6 vocabulary mirrors `state.AllStageNames`
            (ADR-117); the API rejects unknown values with 400. Empty strings are rejected for the
            same reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RetryDeploymentRequest,
) -> Response[DeploymentResponse | Problem]:
    """Retry a failed deployment from a named stage (ADR-117 production-ready follow-on C2).

     Closes the production-ready gap exposed by ADR-117 §C4: a
    deployment that fails partway is restorable via
    `POST /v1/deployments/{id}/retry` with a `from_stage` field.
    The deployment row is duplicated (NOT mutated); the new
    row carries a fresh `stage_state.current` and a fresh
    `stage_state.history` so the dashboard's stage-progression
    timeline (and the CLI's `gregale deploys show <id>` summary)
    reflects the retry as a separate event.

    The closed-6 vocabulary mirrors `state.AllStageNames`
    (ADR-117); the API rejects unknown values with 400.
    Empty strings are rejected for the same reason.

    Auth chain mirrors `POST /v1/apps/{slug}/deployments`:
    `authLimited → requireMFA → requireScope(ScopesDeployWriteSurface)`.
    Returns 202 Accepted with the new deployment row (same
    shape as `POST /v1/apps/{slug}/deployments`).

    Args:
        id (str):
        body (RetryDeploymentRequest): Body for POST /v1/deployments/{id}/retry. Identifies the
            stage the retry should resume from. The closed-6 vocabulary mirrors `state.AllStageNames`
            (ADR-117); the API rejects unknown values with 400. Empty strings are rejected for the
            same reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RetryDeploymentRequest,
) -> DeploymentResponse | Problem | None:
    """Retry a failed deployment from a named stage (ADR-117 production-ready follow-on C2).

     Closes the production-ready gap exposed by ADR-117 §C4: a
    deployment that fails partway is restorable via
    `POST /v1/deployments/{id}/retry` with a `from_stage` field.
    The deployment row is duplicated (NOT mutated); the new
    row carries a fresh `stage_state.current` and a fresh
    `stage_state.history` so the dashboard's stage-progression
    timeline (and the CLI's `gregale deploys show <id>` summary)
    reflects the retry as a separate event.

    The closed-6 vocabulary mirrors `state.AllStageNames`
    (ADR-117); the API rejects unknown values with 400.
    Empty strings are rejected for the same reason.

    Auth chain mirrors `POST /v1/apps/{slug}/deployments`:
    `authLimited → requireMFA → requireScope(ScopesDeployWriteSurface)`.
    Returns 202 Accepted with the new deployment row (same
    shape as `POST /v1/apps/{slug}/deployments`).

    Args:
        id (str):
        body (RetryDeploymentRequest): Body for POST /v1/deployments/{id}/retry. Identifies the
            stage the retry should resume from. The closed-6 vocabulary mirrors `state.AllStageNames`
            (ADR-117); the API rejects unknown values with 400. Empty strings are rejected for the
            same reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
