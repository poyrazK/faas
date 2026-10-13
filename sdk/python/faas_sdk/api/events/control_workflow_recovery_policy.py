from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.control_workflow_recovery_policy_request import ControlWorkflowRecoveryPolicyRequest
from ...models.problem import Problem
from ...models.workflow_recovery_policy_response import WorkflowRecoveryPolicyResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: ControlWorkflowRecoveryPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/recovery-policy/control".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowRecoveryPolicyResponse | None:
    if response.status_code == 200:
        response_200 = WorkflowRecoveryPolicyResponse.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkflowRecoveryPolicyResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: ControlWorkflowRecoveryPolicyRequest,
) -> Response[Problem | WorkflowRecoveryPolicyResponse]:
    """Pause or resume automatic recovery

     Controls enabled automatic recovery without replacing its policy or discarding prepared retries.
    Requires deploy write and MFA. The observed version, update timestamp and pause timestamp fence
    concurrent controls and failure pauses. Resume clears the consecutive failure streak; committed
    children continue while paused.

    Args:
        slug (str):
        body (ControlWorkflowRecoveryPolicyRequest): Operational pause or resume using the last
            observed policy and pause state. Omit expected_paused_at when the observed policy was
            unpaused. Every accepted control updates updated_at and records a trusted audit actor and
            reason without changing the policy version. A retry using stale state returns conflict.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRecoveryPolicyResponse]
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
    client: AuthenticatedClient,
    body: ControlWorkflowRecoveryPolicyRequest,
) -> Problem | WorkflowRecoveryPolicyResponse | None:
    """Pause or resume automatic recovery

     Controls enabled automatic recovery without replacing its policy or discarding prepared retries.
    Requires deploy write and MFA. The observed version, update timestamp and pause timestamp fence
    concurrent controls and failure pauses. Resume clears the consecutive failure streak; committed
    children continue while paused.

    Args:
        slug (str):
        body (ControlWorkflowRecoveryPolicyRequest): Operational pause or resume using the last
            observed policy and pause state. Omit expected_paused_at when the observed policy was
            unpaused. Every accepted control updates updated_at and records a trusted audit actor and
            reason without changing the policy version. A retry using stale state returns conflict.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRecoveryPolicyResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: ControlWorkflowRecoveryPolicyRequest,
) -> Response[Problem | WorkflowRecoveryPolicyResponse]:
    """Pause or resume automatic recovery

     Controls enabled automatic recovery without replacing its policy or discarding prepared retries.
    Requires deploy write and MFA. The observed version, update timestamp and pause timestamp fence
    concurrent controls and failure pauses. Resume clears the consecutive failure streak; committed
    children continue while paused.

    Args:
        slug (str):
        body (ControlWorkflowRecoveryPolicyRequest): Operational pause or resume using the last
            observed policy and pause state. Omit expected_paused_at when the observed policy was
            unpaused. Every accepted control updates updated_at and records a trusted audit actor and
            reason without changing the policy version. A retry using stale state returns conflict.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRecoveryPolicyResponse]
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
    client: AuthenticatedClient,
    body: ControlWorkflowRecoveryPolicyRequest,
) -> Problem | WorkflowRecoveryPolicyResponse | None:
    """Pause or resume automatic recovery

     Controls enabled automatic recovery without replacing its policy or discarding prepared retries.
    Requires deploy write and MFA. The observed version, update timestamp and pause timestamp fence
    concurrent controls and failure pauses. Resume clears the consecutive failure streak; committed
    children continue while paused.

    Args:
        slug (str):
        body (ControlWorkflowRecoveryPolicyRequest): Operational pause or resume using the last
            observed policy and pause state. Omit expected_paused_at when the observed policy was
            unpaused. Every accepted control updates updated_at and records a trusted audit actor and
            reason without changing the policy version. A retry using stale state returns conflict.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRecoveryPolicyResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
