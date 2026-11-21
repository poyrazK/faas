from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.hosted_task_rehearsal_decision import HostedTaskRehearsalDecision
from ...models.hosted_task_rehearsal_plan import HostedTaskRehearsalPlan
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: HostedTaskRehearsalDecision,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/task-tool-source-intents/{id}/rehearsal/decision".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | HostedTaskRehearsalPlan | None:
    if response.status_code == 200:
        response_200 = HostedTaskRehearsalPlan.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 429:
        response_429 = cast(Any, None)
        return response_429

    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | HostedTaskRehearsalPlan]:
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
    body: HostedTaskRehearsalDecision,
) -> Response[Any | HostedTaskRehearsalPlan]:
    """Approve or revoke a hosted Task rehearsal plan

     Record a separate owner decision against the exact plan digest. Approval cannot renew binding
    evidence and produces no runtime effects. Identical retries preserve the original decision time.

    Args:
        id (str):
        body (HostedTaskRehearsalDecision): A separate owner decision against an immutable
            rehearsal plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | HostedTaskRehearsalPlan]
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
    body: HostedTaskRehearsalDecision,
) -> Any | HostedTaskRehearsalPlan | None:
    """Approve or revoke a hosted Task rehearsal plan

     Record a separate owner decision against the exact plan digest. Approval cannot renew binding
    evidence and produces no runtime effects. Identical retries preserve the original decision time.

    Args:
        id (str):
        body (HostedTaskRehearsalDecision): A separate owner decision against an immutable
            rehearsal plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | HostedTaskRehearsalPlan
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
    body: HostedTaskRehearsalDecision,
) -> Response[Any | HostedTaskRehearsalPlan]:
    """Approve or revoke a hosted Task rehearsal plan

     Record a separate owner decision against the exact plan digest. Approval cannot renew binding
    evidence and produces no runtime effects. Identical retries preserve the original decision time.

    Args:
        id (str):
        body (HostedTaskRehearsalDecision): A separate owner decision against an immutable
            rehearsal plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | HostedTaskRehearsalPlan]
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
    body: HostedTaskRehearsalDecision,
) -> Any | HostedTaskRehearsalPlan | None:
    """Approve or revoke a hosted Task rehearsal plan

     Record a separate owner decision against the exact plan digest. Approval cannot renew binding
    evidence and produces no runtime effects. Identical retries preserve the original decision time.

    Args:
        id (str):
        body (HostedTaskRehearsalDecision): A separate owner decision against an immutable
            rehearsal plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | HostedTaskRehearsalPlan
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
