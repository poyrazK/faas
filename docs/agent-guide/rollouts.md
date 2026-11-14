# Releases, rollouts and rollback

Each deploy is a release; its app's release policy decides how it reaches traffic.

- Release new deploys at once (test environments):
    gregale app update <app> --release-policy immediate
- See the current traffic split and canary step:
    gregale release traffic status
- Abort a canary: all traffic back to the earlier release:
    gregale release traffic rollback <app>
- List an app's releases:
    gregale release list --app <app>

Notes:
- The default policy is a health-gated canary when a release is already live.
