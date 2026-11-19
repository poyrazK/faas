# Private Node app on Gregale

This Free-compatible starter runs one internal HTTP service. The loopback
mapping in `compose.yaml` keeps public ingress disabled. The small Docker
image contains Node and its runtime libraries.

Run `gregale start .` to review and deploy the project. After the first
response, edit the greeting in `app/server.js` and select **I've made changes
— review and deploy again** in the same session.

For scripts, use `gregale deploy --project --path . --source=worktree`.
Keep the project and service names from `compose.yaml`. Use the accepted
deployment UUID and app name with `gregale release verify` to check `GET /`.
Use `gregale release wait DEPLOYMENT_UUID --rollout` to follow the rollout to
100% traffic.

[Private Node quickstart](https://gregale.dev/docs/quickstart#deploy-a-private-node-app-on-free)
