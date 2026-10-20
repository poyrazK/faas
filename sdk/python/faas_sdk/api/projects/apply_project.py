from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.apply_response import ApplyResponse
from ...models.problem import Problem
from ...models.project_apply_request import ProjectApplyRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: ProjectApplyRequest,
    plan_token: str | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    params: dict[str, Any] = {}

    params["plan_token"] = plan_token

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects",
        "params": params,
    }

    _kwargs["files"] = body.to_multipart()

    headers["Content-Type"] = "multipart/form-data; boundary=+++"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ApplyResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

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
    body: ProjectApplyRequest,
    plan_token: str | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[ApplyResponse | Problem]:
    """Apply a deploy plan in one transaction.

     Accepts the same multipart body as /scan plus an optional
    `plan_token` query parameter echoing the dry-run token. On
    success the response carries the inserted project_id and
    per-app IDs so the CLI's `--yes` flow can render
    `applied: <slug> → <app_id>`. On quota the response is
    the matching RFC 7807 problem (402 Free crons, 403 apps
    or cron cap) with zero rows inserted.

    The apply handler resolves workload-name → app_id from
    the just-inserted apps and inserts crons in a follow-up
    pass; the quota check ran inside ApplyProjectPlan's Tx.

    Args:
        plan_token (str | Unset):
        idempotency_key (str | Unset):
        body (ProjectApplyRequest): Multipart body for POST /v1/projects (apply). Same shape as
            ProjectScanRequest; the apply handler resolves AppIDs and
            inserts crons in a follow-up pass.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        plan_token=plan_token,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: ProjectApplyRequest,
    plan_token: str | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> ApplyResponse | Problem | None:
    """Apply a deploy plan in one transaction.

     Accepts the same multipart body as /scan plus an optional
    `plan_token` query parameter echoing the dry-run token. On
    success the response carries the inserted project_id and
    per-app IDs so the CLI's `--yes` flow can render
    `applied: <slug> → <app_id>`. On quota the response is
    the matching RFC 7807 problem (402 Free crons, 403 apps
    or cron cap) with zero rows inserted.

    The apply handler resolves workload-name → app_id from
    the just-inserted apps and inserts crons in a follow-up
    pass; the quota check ran inside ApplyProjectPlan's Tx.

    Args:
        plan_token (str | Unset):
        idempotency_key (str | Unset):
        body (ProjectApplyRequest): Multipart body for POST /v1/projects (apply). Same shape as
            ProjectScanRequest; the apply handler resolves AppIDs and
            inserts crons in a follow-up pass.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplyResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        plan_token=plan_token,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ProjectApplyRequest,
    plan_token: str | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[ApplyResponse | Problem]:
    """Apply a deploy plan in one transaction.

     Accepts the same multipart body as /scan plus an optional
    `plan_token` query parameter echoing the dry-run token. On
    success the response carries the inserted project_id and
    per-app IDs so the CLI's `--yes` flow can render
    `applied: <slug> → <app_id>`. On quota the response is
    the matching RFC 7807 problem (402 Free crons, 403 apps
    or cron cap) with zero rows inserted.

    The apply handler resolves workload-name → app_id from
    the just-inserted apps and inserts crons in a follow-up
    pass; the quota check ran inside ApplyProjectPlan's Tx.

    Args:
        plan_token (str | Unset):
        idempotency_key (str | Unset):
        body (ProjectApplyRequest): Multipart body for POST /v1/projects (apply). Same shape as
            ProjectScanRequest; the apply handler resolves AppIDs and
            inserts crons in a follow-up pass.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        plan_token=plan_token,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: ProjectApplyRequest,
    plan_token: str | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> ApplyResponse | Problem | None:
    """Apply a deploy plan in one transaction.

     Accepts the same multipart body as /scan plus an optional
    `plan_token` query parameter echoing the dry-run token. On
    success the response carries the inserted project_id and
    per-app IDs so the CLI's `--yes` flow can render
    `applied: <slug> → <app_id>`. On quota the response is
    the matching RFC 7807 problem (402 Free crons, 403 apps
    or cron cap) with zero rows inserted.

    The apply handler resolves workload-name → app_id from
    the just-inserted apps and inserts crons in a follow-up
    pass; the quota check ran inside ApplyProjectPlan's Tx.

    Args:
        plan_token (str | Unset):
        idempotency_key (str | Unset):
        body (ProjectApplyRequest): Multipart body for POST /v1/projects (apply). Same shape as
            ProjectScanRequest; the apply handler resolves AppIDs and
            inserts crons in a follow-up pass.

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
            plan_token=plan_token,
            idempotency_key=idempotency_key,
        )
    ).parsed
