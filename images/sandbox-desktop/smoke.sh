#!/usr/bin/env bash
# Smoke test of the sandbox desktop image (ADR-20261124075005 rollout step 2):
# start the desktop as the session's uid with no network, wait for it, and
# drive it with gregale-desktop: size, a screenshot, pointer, keys, typing,
# an app launch and its window, and both noVNC ports. Then a warm snapshot's
# prewarm (§3b): it must leave nothing running, and a start after it must
# come up as on a fresh boot.
#
#   bash images/sandbox-desktop/smoke.sh IMAGE_REF [PLATFORM]
set -euo pipefail

image_ref=${1:?image reference is required}
platform=${2:-linux/amd64}
name="gregale-desktop-smoke-$$"
warm="${name}-prewarm"
out=$(mktemp -d)
cleanup() {
  docker rm -f "${name}" "${warm}" >/dev/null 2>&1 || true
  rm -rf "${out}"
}
trap cleanup EXIT

docker run -d --name "${name}" --platform "${platform}" --network none --user 1000:1000 \
  -e HOME=/home/app --shm-size 256m --entrypoint /usr/local/bin/gregale-desktop "${image_ref}" \
  start --resolution 1280x800 >/dev/null

gd() { docker exec "${name}" /usr/local/bin/gregale-desktop "$@"; }

started=$(date +%s%N)
gd wait --timeout 45
echo "ready in $(( ($(date +%s%N) - started) / 1000000 )) ms"

size=$(gd size)
[[ "${size}" == 1280x800 ]] || { echo "size = ${size}, want 1280x800" >&2; exit 1; }

gd screenshot >"${out}/screen.png"
head -c 8 "${out}/screen.png" | od -An -tx1 | grep -q '89 50 4e 47' || { echo "screenshot is not a PNG" >&2; exit 1; }
# A bare root window compresses to a few hundred bytes; the panel and icons
# do not.
(( $(wc -c <"${out}/screen.png") > 4096 )) || { echo "the first screenshot after ready is blank" >&2; exit 1; }
gd screenshot --base64 | base64 -d >"${out}/screen-b64.png"
head -c 8 "${out}/screen-b64.png" | od -An -tx1 | grep -q '89 50 4e 47' || { echo "screenshot --base64 is not a PNG" >&2; exit 1; }
gd screenshot --format jpeg --quality 60 >"${out}/screen.jpg"
head -c 3 "${out}/screen.jpg" | od -An -tx1 | grep -q 'ff d8 ff' || { echo "screenshot is not a JPEG" >&2; exit 1; }
echo "screenshot png $(wc -c <"${out}/screen.png") bytes, jpeg $(wc -c <"${out}/screen.jpg") bytes"
# The PNG is read straight from the X server: ImageMagick must decode it to
# the screen's size and nearly the pixels its own import sees (the panel
# clock may tick in between).
dims=$(docker exec -i "${name}" identify -format '%wx%h' png:- <"${out}/screen.png")
[[ "${dims}" == 1280x800 ]] || { echo "screenshot decodes as ${dims}, want 1280x800" >&2; exit 1; }
docker exec -e DISPLAY=:0 "${name}" sh -c 'import -silent -window root /tmp/im.png && gregale-desktop screenshot >/tmp/gd.png'
differ=$(docker exec "${name}" sh -c 'compare -metric AE /tmp/im.png /tmp/gd.png null: 2>&1' | cut -d' ' -f1)
(( ${differ%.*} < 10240 )) || { echo "screenshot differs from ImageMagick's in ${differ} pixels" >&2; exit 1; }

# Moving or clicking where the pointer already is returns (xdotool's
# mousemove --sync never did).
gd move 100 200
timeout 10 docker exec "${name}" gregale-desktop move 100 200 || { echo "a move to the pointer's spot hung" >&2; exit 1; }
timeout 10 docker exec "${name}" gregale-desktop click 100 200 || { echo "a click at the pointer's spot hung" >&2; exit 1; }
cursor=$(gd cursor)
[[ "${cursor}" == "100 200" ]] || { echo "cursor = ${cursor}, want 100 200" >&2; exit 1; }
gd click 640 400
gd click 640 400 --button right
gd key Escape
gd scroll down 3
gd drag 10 10 50 50

# Bad input exits 2 and does nothing.
set +e
gd click 5000 10 2>/dev/null; [[ $? == 2 ]] || { echo "an off-screen click did not exit 2" >&2; exit 1; }
gd key '$(reboot)' 2>/dev/null; [[ $? == 2 ]] || { echo "a bad key did not exit 2" >&2; exit 1; }
set -e

gd launch mousepad
for _ in $(seq 1 40); do
  if gd windows --app mousepad | grep -q .; then break; fi
  sleep 0.25
done
gd windows --app mousepad | grep -q . || { echo "mousepad has no window" >&2; gd windows >&2; exit 1; }
sleep 1
gd type -- 'hello from gregale'
gd key ctrl+a ctrl+c
window=$(gd window)
echo "focused: ${window}"

gd launch chromium -- about:blank
for _ in $(seq 1 60); do
  if gd windows --app chromium | grep -q .; then break; fi
  sleep 0.5
done
gd windows --app chromium | grep -q . || { echo "chromium has no window" >&2; exit 1; }

for port in 6080 6081; do
  body=$(docker exec "${name}" python3 -c "import urllib.request; print(urllib.request.urlopen('http://127.0.0.1:${port}/').read().decode())")
  grep -q 'vnc.html?autoconnect=1' <<<"${body}" || { echo "port ${port} does not serve noVNC" >&2; exit 1; }
done

echo "processes (RSS MB):"
docker exec "${name}" ps -eo rss=,comm= | awk '{t+=$1; printf "  %6.1f %s\n", $1/1024, $2} END {printf "  %6.1f total\n", t/1024}' | sort -n | tail -12

# Prewarm, then start again in the same container, as a restored warm
# snapshot does. PID 1 is the container's sleep, which reaps nothing, so the
# zombies it is left do not count; guest-init reaps them in a VM.
docker run -d --name "${warm}" --platform "${platform}" --network none --user 1000:1000 \
  -e HOME=/home/app --shm-size 256m --entrypoint sleep "${image_ref}" infinity >/dev/null
started=$(date +%s%N)
docker exec "${warm}" /usr/local/bin/gregale-desktop prewarm
echo "prewarm in $(( ($(date +%s%N) - started) / 1000000 )) ms"
left=$(docker exec "${warm}" ps -u 1000 -o pid=,stat=,comm= | awk '$1 != 1 && $2 !~ /^Z/ && $3 != "ps"')
[[ -z "${left}" ]] || { echo "processes outlived the prewarm: ${left}" >&2; exit 1; }
if docker exec "${warm}" sh -c 'test -e /tmp/.X11-unix/X0 || test -e /tmp/.X0-lock || test -e /home/app/.config/chromium'; then
  echo "the prewarm left the X socket, lock or Chromium's profile" >&2
  exit 1
fi
docker exec -d "${warm}" /usr/local/bin/gregale-desktop start --resolution 800x600
started=$(date +%s%N)
docker exec "${warm}" /usr/local/bin/gregale-desktop wait --timeout 45
echo "ready after prewarm in $(( ($(date +%s%N) - started) / 1000000 )) ms"
size=$(docker exec "${warm}" /usr/local/bin/gregale-desktop size)
[[ "${size}" == 800x600 ]] || { echo "size after prewarm = ${size}, want 800x600" >&2; exit 1; }
(( $(docker exec "${warm}" /usr/local/bin/gregale-desktop screenshot | wc -c) > 4096 )) ||
  { echo "the desktop after prewarm is blank" >&2; exit 1; }
echo "sandbox-desktop smoke ok"
