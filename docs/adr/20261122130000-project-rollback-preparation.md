# Prepare project rollback targets before cutover

Status: Accepted

Project rollback preflight can identify missing artifacts, changed deployments, and release commands. A cold historical deployment still needs to start before Gregale can verify its bindings and runtime readiness. Preparing services one at a time can leave the project partially rolled back while a downstream target waits on one of those checks.

The project rollback worker admits up to four targets at a time for zero-traffic preparation. Already prepared targets stay available while their peers warm. The worker records progress and binding blockers against the exact target in the project receipt. Preparation and cutover share a ten-minute window. When it expires, the worker closes the cutover gate and releases unused preparations while preserving blocker details.

The checked rollback worker uses the existing artifact, release, binding, and readiness checks. PostgreSQL guards traffic increases for project-owned targets. Before each cutover the store checks the saved serving revisions and the preparation of every pending peer. Cutovers still run callers first, one service at a time. A concurrent change after cutover begins can suspend the remaining steps, with completed steps preserved in the receipt. A resume starts a fresh preparation window for the remaining steps.
