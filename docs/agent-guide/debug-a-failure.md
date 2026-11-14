# Debug a failed deploy or a failing app

Every failure names its code and a fix; start from the fix.

- Read an app's logs:
    gregale logs <app>
- Read a failed deployment's build and startup logs:
    gregale logs <app> --deployment <deployment>
- See an app's state, park reason and recommendations:
    gregale inspect <app>
- Run a command against the live deployment:
    gregale app <app> exec -- <command>
- Check the source locally:
    gregale doctor

Notes:
- app_startup_timeout: the app did not answer readiness in 30 s; see `gregale guide health-checks`.
- liveness_exhausted: the app kept failing its health check while serving and was parked.
- app_runtime_oom: raise memory with `gregale app <app> --ram <MB>`.
