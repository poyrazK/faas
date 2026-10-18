#!/usr/bin/env bash
set -euo pipefail

apt_root="${APT_ROOT:-/etc/apt}"
if [[ "$apt_root" == /etc/apt ]]; then
  sudo find "$apt_root" -type f \
    \( -name '*.sources' -o -name '*.list' -o -name sources.list -o -name apt-mirrors.txt \) \
    -exec perl -pi -e 's#http://azure.archive.ubuntu.com/ubuntu#https://archive.ubuntu.com/ubuntu#g' {} +
else
  find "$apt_root" -type f \
    \( -name '*.sources' -o -name '*.list' -o -name sources.list -o -name apt-mirrors.txt \) \
    -exec perl -pi -e 's#http://azure.archive.ubuntu.com/ubuntu#https://archive.ubuntu.com/ubuntu#g' {} +
fi
