# Deploy to DigitalOcean — FaaS Control Plane

Deploys the non-KVM parts of the platform on a DigitalOcean Droplet for
development and testing. `vmmd` and `builderd` are **not deployed** (no
`/dev/kvm` on DO Droplets) — VM lifecycle operations return errors, which is
expected.

## What runs

| Daemon | Port / Socket | Purpose |
|--------|---------------|---------|
| `gatewayd` | `:8080` (public HTTP) | Edge proxy, routing, rate limits |
| `apid` | `127.0.0.1:8081` | REST API, auth, quotas |
| `schedd` | `/run/faas/schedd.sock` | Scheduler, instance state machine |
| `imaged` | (event-driven) | OCI pull + ext4 layer builder |
| `meterd` | (timer loops) | Metering, quota, billing |
| `githubd` | `127.0.0.1:8083` + socket | GitHub App OAuth, webhooks |
| Postgres 15 | Unix socket | Database |

## Quick start

### 1. Create a Droplet

- **Image:** Ubuntu 24.04 LTS
- **Plan:** Regular 4 GB / 2 vCPU ($24/mo) — the control plane is lightweight
- **Region:** Pick closest to you
- **Auth:** Your SSH key

### 2. Bootstrap

SSH into the Droplet and run:

```bash
# Option A: From the repo (if you've cloned it on the Droplet)
sudo bash deploy/digitalocean/bootstrap.sh

# Option B: One-liner from GitHub
curl -sSf https://raw.githubusercontent.com/poyrazK/faas/main/deploy/digitalocean/bootstrap.sh | sudo bash
```

The script:
- Installs Postgres 15, Go, system deps
- Creates system users (`faas`, `faas-apid`, `faas-schedd`, …)
- Clones the repo to `/opt/faas/src`, builds all binaries to `/opt/faas/bin`
- Drops TOML configs + `sealed.env` to `/etc/faas/`
- Installs systemd units and slices
- Runs DB migrations
- Generates an SSH deploy key for GitHub Actions CD
- Starts all services

### 3. Verify

```bash
# From the Droplet
curl http://127.0.0.1:8081/healthz                              # apid
curl http://127.0.0.1:9090/healthz                              # gatewayd
curl -H "Authorization: Bearer fp_dev_localtest" \
     http://127.0.0.1:8081/v1/apps                              # API auth

# From your laptop
curl http://<DROPLET_IP>:8080/status                            # status page
open http://<DROPLET_IP>:8080/dashboard/                        # dashboard UI
```

### 4. Set up GitHub Actions CD

After `bootstrap.sh` finishes, it prints a deploy SSH key. Add these to
your GitHub repo (Settings → Secrets and variables → Actions):

| Secret | Value |
|--------|-------|
| `DO_SSH_KEY` | The ed25519 private key printed by bootstrap.sh |
| `DO_HOST` | Droplet public IP address |

Now every push to `main` that passes CI will auto-deploy to the Droplet.

### 5. Set up GitHub App (optional)

1. Go to https://github.com/settings/apps → **New GitHub App**
2. Configure:
   - **Homepage URL:** `http://<DROPLET_IP>:8080`
   - **Callback URL:** `http://<DROPLET_IP>:8080/oauth/github/callback`
   - **Webhook URL:** `http://<DROPLET_IP>:8080/webhooks/github`
   - **Webhook secret:** Generate one, save it
   - **Permissions:** Contents (read), Checks (write), Metadata (read)
   - **Events:** Push, Check suite
3. After creation, note the **App ID** and download the **private key** (.pem)
4. On the Droplet:
   ```bash
   # Copy the .pem file
   sudo cp github-app.pem /etc/faas/secrets/github-app.pem
   sudo chmod 0400 /etc/faas/secrets/github-app.pem
   sudo chown root:faas /etc/faas/secrets/github-app.pem

   # Add to sealed.env
   sudo tee -a /etc/faas/sealed.env <<EOF
   FAAS_GITHUB_APP_ID=<your-app-id>
   FAAS_GITHUB_APP_KEY_PATH=/etc/faas/secrets/github-app.pem
   FAAS_GITHUB_WEBHOOK_SECRET=<your-webhook-secret>
   EOF

   # Restart githubd
   sudo systemctl restart faas-githubd
   ```

## Manual redeploy

If you prefer not to use GitHub Actions CD, SSH into the Droplet and run:

```bash
sudo bash /opt/faas/src/deploy/digitalocean/deploy.sh
```

This pulls latest source, rebuilds, migrates, and restarts.

## Logs & debugging

```bash
# Follow all FaaS service logs
journalctl -u 'faas-*' -f

# Single service
journalctl -u faas-apid -n 50

# Service status
systemctl status faas-apid faas-schedd faas-gatewayd faas-imaged faas-meterd faas-githubd

# Postgres
sudo -u postgres psql faas
```

## What does NOT work (expected)

- **`faas deploy`** — goes through apid/imaged but fails at snapshot/wake (no vmmd)
- **Wake requests** — schedd can't dial vmmd, returns 503
- **Builds** — builderd is not deployed (needs vmmd for builder VMs)
- **Snapshot/restore latency** — no Firecracker on DO

These require a KVM-capable host (Hetzner Cloud CCX or the production EX44).

## File layout on the Droplet

```
/opt/faas/bin/          compiled daemons (apid, schedd, …)
/opt/faas/src/          git checkout of the repo
/etc/faas/              TOML configs + sealed.env
/etc/faas/secrets/      GitHub App key, age key (mode 0400)
/run/faas/              Unix sockets (runtime, tmpfs)
/var/log/faas/          daemon logs
/var/spool/faas/        async work queue
/srv/fc/snap/           snapshot storage (mostly empty on DO)
```
