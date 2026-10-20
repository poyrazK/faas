from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.rekey_progress import RekeyProgress
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/secrets/rekey-progress",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RekeyProgress | None:
    if response.status_code == 200:
        response_200 = RekeyProgress.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
) -> Response[Problem | RekeyProgress]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RekeyProgress]:
    r"""Read the cumulative rekey walk progress (admin-only).

     Returns the latest RekeyProgress snapshot the apid rekey
    runner has written — either to the in-process atomic pointer
    (memory-only mode) or to FAAS_REKEY_PROGRESS_FILE on disk.
    Operators poll this endpoint to monitor the walk after a
    host identity rotation; the response shape mirrors
    rekey.RekeyProgress exactly so a future operator tool
    (e.g. `gregale rekey status`) can decode without a parallel
    type.

    `total` is the running count of rows observed so far; it can
    grow as the walk paginates through (account_id, app_id, key)
    order. `rekeyed` + `skipped` should approach `total` once
    the walk drains. `failed` should stay at zero; a non-zero
    value means the unseal step threw for at least one row —
    the operationally safe recovery is `git rm
    migrations/*reserve_slot.sql`-style idempotent re-trigger
    (toggle FAAS_REKEY_ENABLED and restart apid; the seen-set
    inside Replayer dedupes already-done rows).

    When the runner is disabled (FAAS_REKEY_ENABLED unset), the
    endpoint returns 503 with code `rekey_disabled` so an
    operator can distinguish \"no work yet\" from \"feature off\".
    If FAAS_REKEY_ENABLED=true is set but no host age identities
    loaded (mfaIdentities() empty — typically FAAS_HOST_AGE_IDENTITY_PATH
    unset), the endpoint returns 503 with the distinct code
    `rekey_no_identities` (PR #825 follow-up); this avoids the
    misleading \"set FAAS_REKEY_ENABLED and restart\" detail when
    the operator already opted in.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RekeyProgress]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RekeyProgress | None:
    r"""Read the cumulative rekey walk progress (admin-only).

     Returns the latest RekeyProgress snapshot the apid rekey
    runner has written — either to the in-process atomic pointer
    (memory-only mode) or to FAAS_REKEY_PROGRESS_FILE on disk.
    Operators poll this endpoint to monitor the walk after a
    host identity rotation; the response shape mirrors
    rekey.RekeyProgress exactly so a future operator tool
    (e.g. `gregale rekey status`) can decode without a parallel
    type.

    `total` is the running count of rows observed so far; it can
    grow as the walk paginates through (account_id, app_id, key)
    order. `rekeyed` + `skipped` should approach `total` once
    the walk drains. `failed` should stay at zero; a non-zero
    value means the unseal step threw for at least one row —
    the operationally safe recovery is `git rm
    migrations/*reserve_slot.sql`-style idempotent re-trigger
    (toggle FAAS_REKEY_ENABLED and restart apid; the seen-set
    inside Replayer dedupes already-done rows).

    When the runner is disabled (FAAS_REKEY_ENABLED unset), the
    endpoint returns 503 with code `rekey_disabled` so an
    operator can distinguish \"no work yet\" from \"feature off\".
    If FAAS_REKEY_ENABLED=true is set but no host age identities
    loaded (mfaIdentities() empty — typically FAAS_HOST_AGE_IDENTITY_PATH
    unset), the endpoint returns 503 with the distinct code
    `rekey_no_identities` (PR #825 follow-up); this avoids the
    misleading \"set FAAS_REKEY_ENABLED and restart\" detail when
    the operator already opted in.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RekeyProgress
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RekeyProgress]:
    r"""Read the cumulative rekey walk progress (admin-only).

     Returns the latest RekeyProgress snapshot the apid rekey
    runner has written — either to the in-process atomic pointer
    (memory-only mode) or to FAAS_REKEY_PROGRESS_FILE on disk.
    Operators poll this endpoint to monitor the walk after a
    host identity rotation; the response shape mirrors
    rekey.RekeyProgress exactly so a future operator tool
    (e.g. `gregale rekey status`) can decode without a parallel
    type.

    `total` is the running count of rows observed so far; it can
    grow as the walk paginates through (account_id, app_id, key)
    order. `rekeyed` + `skipped` should approach `total` once
    the walk drains. `failed` should stay at zero; a non-zero
    value means the unseal step threw for at least one row —
    the operationally safe recovery is `git rm
    migrations/*reserve_slot.sql`-style idempotent re-trigger
    (toggle FAAS_REKEY_ENABLED and restart apid; the seen-set
    inside Replayer dedupes already-done rows).

    When the runner is disabled (FAAS_REKEY_ENABLED unset), the
    endpoint returns 503 with code `rekey_disabled` so an
    operator can distinguish \"no work yet\" from \"feature off\".
    If FAAS_REKEY_ENABLED=true is set but no host age identities
    loaded (mfaIdentities() empty — typically FAAS_HOST_AGE_IDENTITY_PATH
    unset), the endpoint returns 503 with the distinct code
    `rekey_no_identities` (PR #825 follow-up); this avoids the
    misleading \"set FAAS_REKEY_ENABLED and restart\" detail when
    the operator already opted in.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RekeyProgress]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RekeyProgress | None:
    r"""Read the cumulative rekey walk progress (admin-only).

     Returns the latest RekeyProgress snapshot the apid rekey
    runner has written — either to the in-process atomic pointer
    (memory-only mode) or to FAAS_REKEY_PROGRESS_FILE on disk.
    Operators poll this endpoint to monitor the walk after a
    host identity rotation; the response shape mirrors
    rekey.RekeyProgress exactly so a future operator tool
    (e.g. `gregale rekey status`) can decode without a parallel
    type.

    `total` is the running count of rows observed so far; it can
    grow as the walk paginates through (account_id, app_id, key)
    order. `rekeyed` + `skipped` should approach `total` once
    the walk drains. `failed` should stay at zero; a non-zero
    value means the unseal step threw for at least one row —
    the operationally safe recovery is `git rm
    migrations/*reserve_slot.sql`-style idempotent re-trigger
    (toggle FAAS_REKEY_ENABLED and restart apid; the seen-set
    inside Replayer dedupes already-done rows).

    When the runner is disabled (FAAS_REKEY_ENABLED unset), the
    endpoint returns 503 with code `rekey_disabled` so an
    operator can distinguish \"no work yet\" from \"feature off\".
    If FAAS_REKEY_ENABLED=true is set but no host age identities
    loaded (mfaIdentities() empty — typically FAAS_HOST_AGE_IDENTITY_PATH
    unset), the endpoint returns 503 with the distinct code
    `rekey_no_identities` (PR #825 follow-up); this avoids the
    misleading \"set FAAS_REKEY_ENABLED and restart\" detail when
    the operator already opted in.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RekeyProgress
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
