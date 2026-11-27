from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.resume_stored_workflow_failure_pause_request import ResumeStoredWorkflowFailurePauseRequest
from ...models.stored_workflow_failure_policy_response import StoredWorkflowFailurePolicyResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    *,
    body: ResumeStoredWorkflowFailurePauseRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/workflows/stored/{name}/failure-policy/resume".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | StoredWorkflowFailurePolicyResponse | None:
    if response.status_code == 200:
        response_200 = StoredWorkflowFailurePolicyResponse.from_dict(response.json())

        return response_200

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | StoredWorkflowFailurePolicyResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: ResumeStoredWorkflowFailurePauseRequest,
) -> Response[Problem | StoredWorkflowFailurePolicyResponse]:
    """Explicitly clear a failure pause after inspecting retained work.

     Requires deploy write scope and the current positive pause generation. Starts a fresh monitoring
    epoch and re-arms schedules without catch-up for the paused interval. Existing runs continue and
    captured events remain subject to their routing retention; events received without an eligible
    recipient are not replayed. This action does not change manual pause intent.

    Args:
        slug (str):
        name (str):
        body (ResumeStoredWorkflowFailurePauseRequest): Explicit failure guard resume guarded by
            its current generation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StoredWorkflowFailurePolicyResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: ResumeStoredWorkflowFailurePauseRequest,
) -> Problem | StoredWorkflowFailurePolicyResponse | None:
    """Explicitly clear a failure pause after inspecting retained work.

     Requires deploy write scope and the current positive pause generation. Starts a fresh monitoring
    epoch and re-arms schedules without catch-up for the paused interval. Existing runs continue and
    captured events remain subject to their routing retention; events received without an eligible
    recipient are not replayed. This action does not change manual pause intent.

    Args:
        slug (str):
        name (str):
        body (ResumeStoredWorkflowFailurePauseRequest): Explicit failure guard resume guarded by
            its current generation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StoredWorkflowFailurePolicyResponse
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: ResumeStoredWorkflowFailurePauseRequest,
) -> Response[Problem | StoredWorkflowFailurePolicyResponse]:
    """Explicitly clear a failure pause after inspecting retained work.

     Requires deploy write scope and the current positive pause generation. Starts a fresh monitoring
    epoch and re-arms schedules without catch-up for the paused interval. Existing runs continue and
    captured events remain subject to their routing retention; events received without an eligible
    recipient are not replayed. This action does not change manual pause intent.

    Args:
        slug (str):
        name (str):
        body (ResumeStoredWorkflowFailurePauseRequest): Explicit failure guard resume guarded by
            its current generation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StoredWorkflowFailurePolicyResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: ResumeStoredWorkflowFailurePauseRequest,
) -> Problem | StoredWorkflowFailurePolicyResponse | None:
    """Explicitly clear a failure pause after inspecting retained work.

     Requires deploy write scope and the current positive pause generation. Starts a fresh monitoring
    epoch and re-arms schedules without catch-up for the paused interval. Existing runs continue and
    captured events remain subject to their routing retention; events received without an eligible
    recipient are not replayed. This action does not change manual pause intent.

    Args:
        slug (str):
        name (str):
        body (ResumeStoredWorkflowFailurePauseRequest): Explicit failure guard resume guarded by
            its current generation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StoredWorkflowFailurePolicyResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            body=body,
        )
    ).parsed
