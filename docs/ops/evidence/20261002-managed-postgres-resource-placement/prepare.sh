set -eu
test -c /dev/kvm
test -f /etc/faas/builder-acceptance-host
test "$(uname -m)" = x86_64
for service in faas-vmmd.service faas-builderd.service faas-imaged.service faas-gatewayd-internal.service; do
 if sudo systemctl is-active --quiet "$service"; then exit 1; fi
done
test ! -e /var/tmp/faas-metal-smoke-managed-pg-resource-placement-ca1230ac
sudo install -d -m 0755 /var/tmp/faas-metal-smoke-managed-pg-resource-placement-ca1230ac
sudo /usr/local/sbin/faas-canary-artifacts register --path /var/tmp/faas-metal-smoke-managed-pg-resource-placement-ca1230ac --run-id managed-pg-resource-placement-ca1230ac --kind native-metal
sudo cp -a /var/cache/faas-metal-smoke/go-mod /var/tmp/faas-metal-smoke-managed-pg-resource-placement-ca1230ac/go-mod
sudo cp -a /var/cache/faas-metal-smoke/go-build /var/tmp/faas-metal-smoke-managed-pg-resource-placement-ca1230ac/go-build
sudo chown "$(id -u):$(id -g)" /var/tmp/faas-metal-smoke-managed-pg-resource-placement-ca1230ac
sudo du -sh /var/tmp/faas-metal-smoke-managed-pg-resource-placement-ca1230ac
df -h /
