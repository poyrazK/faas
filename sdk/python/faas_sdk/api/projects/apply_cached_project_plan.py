from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.apply_cached_project_plan_request import ApplyCachedProjectPlanRequest
from ...models.apply_response import ApplyResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: ApplyCachedProjectPlanRequest,
    deployment_attempt_id: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(deployment_attempt_id, Unset):
        headers["Deployment-Attempt-ID"] = deployment_attempt_id

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/apply-plan",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ApplyResponse.from_dict(response.json())

        return response_200

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ApplyResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ApplyCachedProjectPlanRequest,
    deployment_attempt_id: str | Unset = UNSET,
) -> Response[ApplyResponse | Problem]:
    """Deploy an account-owned reviewed project source without another upload.

     Applies the exact archive retained by a project scan, not the current
    head of a mutable Git ref. The normal scanner and apply admission gates
    run again. The account-scoped source expires after one hour; rescan on
    preview_expired. Initial secrets require secrets:write or admin and are
    sealed before any deployment is enqueued. Failed configuration starts
    no builds; saved apps and any completed configuration remain available
    for correction. Values are never included in responses or scan plans.

    Args:
        deployment_attempt_id (str | Unset):
        body (ApplyCachedProjectPlanRequest): Applies an account-owned reviewed project archive
            with selected workloads and optional initial sealed configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        deployment_attempt_id=deployment_attempt_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: ApplyCachedProjectPlanRequest,
    deployment_attempt_id: str | Unset = UNSET,
) -> ApplyResponse | Problem | None:
    """Deploy an account-owned reviewed project source without another upload.

     Applies the exact archive retained by a project scan, not the current
    head of a mutable Git ref. The normal scanner and apply admission gates
    run again. The account-scoped source expires after one hour; rescan on
    preview_expired. Initial secrets require secrets:write or admin and are
    sealed before any deployment is enqueued. Failed configuration starts
    no builds; saved apps and any completed configuration remain available
    for correction. Values are never included in responses or scan plans.

    Args:
        deployment_attempt_id (str | Unset):
        body (ApplyCachedProjectPlanRequest): Applies an account-owned reviewed project archive
            with selected workloads and optional initial sealed configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplyResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        deployment_attempt_id=deployment_attempt_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ApplyCachedProjectPlanRequest,
    deployment_attempt_id: str | Unset = UNSET,
) -> Response[ApplyResponse | Problem]:
    """Deploy an account-owned reviewed project source without another upload.

     Applies the exact archive retained by a project scan, not the current
    head of a mutable Git ref. The normal scanner and apply admission gates
    run again. The account-scoped source expires after one hour; rescan on
    preview_expired. Initial secrets require secrets:write or admin and are
    sealed before any deployment is enqueued. Failed configuration starts
    no builds; saved apps and any completed configuration remain available
    for correction. Values are never included in responses or scan plans.

    Args:
        deployment_attempt_id (str | Unset):
        body (ApplyCachedProjectPlanRequest): Applies an account-owned reviewed project archive
            with selected workloads and optional initial sealed configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        deployment_attempt_id=deployment_attempt_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: ApplyCachedProjectPlanRequest,
    deployment_attempt_id: str | Unset = UNSET,
) -> ApplyResponse | Problem | None:
    """Deploy an account-owned reviewed project source without another upload.

     Applies the exact archive retained by a project scan, not the current
    head of a mutable Git ref. The normal scanner and apply admission gates
    run again. The account-scoped source expires after one hour; rescan on
    preview_expired. Initial secrets require secrets:write or admin and are
    sealed before any deployment is enqueued. Failed configuration starts
    no builds; saved apps and any completed configuration remain available
    for correction. Values are never included in responses or scan plans.

    Args:
        deployment_attempt_id (str | Unset):
        body (ApplyCachedProjectPlanRequest): Applies an account-owned reviewed project archive
            with selected workloads and optional initial sealed configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplyResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            deployment_attempt_id=deployment_attempt_id,
        )
    ).parsed
