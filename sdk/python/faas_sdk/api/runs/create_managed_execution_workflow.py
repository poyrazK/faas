from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_managed_execution_workflow_request import CreateManagedExecutionWorkflowRequest
from ...models.managed_execution_workflow_response import ManagedExecutionWorkflowResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: CreateManagedExecutionWorkflowRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/execution-workflows",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ManagedExecutionWorkflowResponse | Problem | None:
    if response.status_code == 202:
        response_202 = ManagedExecutionWorkflowResponse.from_dict(response.json())

        return response_202

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
) -> Response[Any | ManagedExecutionWorkflowResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: CreateManagedExecutionWorkflowRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Any | ManagedExecutionWorkflowResponse | Problem]:
    """Submit a server-managed Runs DAG.

     Stores a bounded dependency graph encrypted with the host age identity
    and admits each step as a separate disposable Run after its dependencies
    succeed. Independent steps may run in parallel up to the submitted
    workflow limit and the account's concurrent Run limit. Continuation is
    owned by the control plane and survives CLI disconnects and apid
    restarts. Source and input are never returned; the encrypted plan is
    erased when the workflow reaches a terminal state. Requires
    `runs:write`, `deploy:write`, or `admin` and an enabled Runs control
    plane with the host age recipient and identity configured. Each account
    may have at most 16 active managed workflows; terminal workflows
    release their encrypted plan and queue slot.

    Args:
        idempotency_key (str | Unset):
        body (CreateManagedExecutionWorkflowRequest): Encrypted, server-managed DAG. Every step
            runs in its own disposable
            Run. Independent steps may run in parallel up to max_parallel_steps
            and the account's concurrent Run limit. The full JSON body must not
            exceed 4 MiB. An account can have at most 16 active managed workflows.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ManagedExecutionWorkflowResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: CreateManagedExecutionWorkflowRequest,
    idempotency_key: str | Unset = UNSET,
) -> Any | ManagedExecutionWorkflowResponse | Problem | None:
    """Submit a server-managed Runs DAG.

     Stores a bounded dependency graph encrypted with the host age identity
    and admits each step as a separate disposable Run after its dependencies
    succeed. Independent steps may run in parallel up to the submitted
    workflow limit and the account's concurrent Run limit. Continuation is
    owned by the control plane and survives CLI disconnects and apid
    restarts. Source and input are never returned; the encrypted plan is
    erased when the workflow reaches a terminal state. Requires
    `runs:write`, `deploy:write`, or `admin` and an enabled Runs control
    plane with the host age recipient and identity configured. Each account
    may have at most 16 active managed workflows; terminal workflows
    release their encrypted plan and queue slot.

    Args:
        idempotency_key (str | Unset):
        body (CreateManagedExecutionWorkflowRequest): Encrypted, server-managed DAG. Every step
            runs in its own disposable
            Run. Independent steps may run in parallel up to max_parallel_steps
            and the account's concurrent Run limit. The full JSON body must not
            exceed 4 MiB. An account can have at most 16 active managed workflows.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ManagedExecutionWorkflowResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: CreateManagedExecutionWorkflowRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Any | ManagedExecutionWorkflowResponse | Problem]:
    """Submit a server-managed Runs DAG.

     Stores a bounded dependency graph encrypted with the host age identity
    and admits each step as a separate disposable Run after its dependencies
    succeed. Independent steps may run in parallel up to the submitted
    workflow limit and the account's concurrent Run limit. Continuation is
    owned by the control plane and survives CLI disconnects and apid
    restarts. Source and input are never returned; the encrypted plan is
    erased when the workflow reaches a terminal state. Requires
    `runs:write`, `deploy:write`, or `admin` and an enabled Runs control
    plane with the host age recipient and identity configured. Each account
    may have at most 16 active managed workflows; terminal workflows
    release their encrypted plan and queue slot.

    Args:
        idempotency_key (str | Unset):
        body (CreateManagedExecutionWorkflowRequest): Encrypted, server-managed DAG. Every step
            runs in its own disposable
            Run. Independent steps may run in parallel up to max_parallel_steps
            and the account's concurrent Run limit. The full JSON body must not
            exceed 4 MiB. An account can have at most 16 active managed workflows.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ManagedExecutionWorkflowResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: CreateManagedExecutionWorkflowRequest,
    idempotency_key: str | Unset = UNSET,
) -> Any | ManagedExecutionWorkflowResponse | Problem | None:
    """Submit a server-managed Runs DAG.

     Stores a bounded dependency graph encrypted with the host age identity
    and admits each step as a separate disposable Run after its dependencies
    succeed. Independent steps may run in parallel up to the submitted
    workflow limit and the account's concurrent Run limit. Continuation is
    owned by the control plane and survives CLI disconnects and apid
    restarts. Source and input are never returned; the encrypted plan is
    erased when the workflow reaches a terminal state. Requires
    `runs:write`, `deploy:write`, or `admin` and an enabled Runs control
    plane with the host age recipient and identity configured. Each account
    may have at most 16 active managed workflows; terminal workflows
    release their encrypted plan and queue slot.

    Args:
        idempotency_key (str | Unset):
        body (CreateManagedExecutionWorkflowRequest): Encrypted, server-managed DAG. Every step
            runs in its own disposable
            Run. Independent steps may run in parallel up to max_parallel_steps
            and the account's concurrent Run limit. The full JSON body must not
            exceed 4 MiB. An account can have at most 16 active managed workflows.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ManagedExecutionWorkflowResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
