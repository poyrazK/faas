set -eu
test -c /dev/kvm
test -f /etc/faas/builder-acceptance-host
test "$(uname -m)" = x86_64
for service in faas-vmmd.service faas-builderd.service faas-imaged.service faas-gatewayd-internal.service; do
 if sudo systemctl is-active --quiet "$service"; then exit 1; fi
done
test ! -e /var/tmp/faas-metal-smoke-managed-pg-resource-journal-4e4f5a64
sudo install -d -m 0755 /var/tmp/faas-metal-smoke-managed-pg-resource-journal-4e4f5a64
sudo /usr/local/sbin/faas-canary-artifacts register --path /var/tmp/faas-metal-smoke-managed-pg-resource-journal-4e4f5a64 --run-id managed-pg-resource-journal-4e4f5a64 --kind native-metal
sudo mount -t tmpfs -o size=6G,mode=0755 tmpfs /var/tmp/faas-metal-smoke-managed-pg-resource-journal-4e4f5a64
sudo chown "$(id -u):$(id -g)" /var/tmp/faas-metal-smoke-managed-pg-resource-journal-4e4f5a64
sudo cp -a /var/cache/faas-metal-smoke/go-mod /var/tmp/faas-metal-smoke-managed-pg-resource-journal-4e4f5a64/go-mod
findmnt -rn -M /var/tmp/faas-metal-smoke-managed-pg-resource-journal-4e4f5a64 -o TARGET,FSTYPE,OPTIONS
sudo du -sh /var/tmp/faas-metal-smoke-managed-pg-resource-journal-4e4f5a64/go-mod
