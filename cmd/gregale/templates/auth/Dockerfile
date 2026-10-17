# GoTrue (supabase/auth) v2.197.0, linux/amd64 manifest of the index
# sha256:1736a63078f5922b198c4cbe50f80ab9a2d3b54fe8b7b6cfb2e9dc5dbbc12c6b.
# Gregale builders are x86_64; the index's arm64 variant ships an unusable
# binary, so the platform manifest is pinned directly (ADR-1190).
FROM supabase/gotrue@sha256:839f529492d116b4e8b7777c953a27c381d34c15a744b1bcefde5eefaa1f9f9f AS gotrue
FROM node:22-bookworm-slim
WORKDIR /app
COPY --from=gotrue /usr/local/bin/auth /usr/local/bin/gotrue-auth
COPY --from=gotrue /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=gotrue /usr/local/etc/auth/migrations /app/gotrue-migrations
COPY migrations.sha256 /app/migrations.sha256
COPY overlay/ /app/overlay/
# Every upstream migration must match the pinned release byte for byte,
# including the one file the overlay replaces, before the overlay applies.
RUN cd /app/gotrue-migrations \
 && sha256sum --check --strict --quiet /app/migrations.sha256 \
 && test "$(ls | wc -l)" -eq "$(wc -l < /app/migrations.sha256)" \
 && cp /app/overlay/*.sql /app/gotrue-migrations/ \
 && rm -r /app/overlay /app/migrations.sha256
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts
COPY config.mjs server.mjs authz.mjs admin.mjs email.mjs events.mjs logs.mjs oauth.mjs orgsso.mjs realtime.mjs retention.mjs scim.mjs sms.mjs sso.mjs storage.mjs workflows.mjs migrate.mjs ./
COPY sql/ ./sql/
COPY ui/ ./ui/
EXPOSE 8080
CMD ["node", "server.mjs"]
