# The sandbox desktop image (ADR-20261124075005).
#
# Sessions created with preset desktop boot this image as an image session
# (apid FAAS_SANDBOX_DESKTOP_IMAGE names its published digest). guest-init
# stays PID 1; agentd's start command, gregale-desktop start, runs as the
# session's user (uid 1000) and brings up Xvfb on :0, Xfce, two x11vnc
# servers on loopback (one view only) and noVNC on 6080 and 6081. Agents
# act on the screen with gregale-desktop (xdotool, the X protocol, ImageMagick,
# wmctrl). Nothing is setuid; the microVM is the sandbox. There is no
# xfce4-session, systemd user session or x11-xserver-utils (and so no cpp):
# gregale-desktop starts Xfce's parts itself under dbus-run-session.
FROM mirror.gcr.io/library/debian:13-slim@sha256:7378260c0a098d27ac206225db5e7d92b8c7c84ea7afccc86eeb513756bbd392
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates bash procps python3 dbus dbus-x11 \
      xvfb x11-utils xauth xdg-utils \
      xfwm4 xfce4-panel xfdesktop4 xfce4-settings xfconf thunar xfce4-terminal mousepad \
      adwaita-icon-theme-legacy hicolor-icon-theme \
      x11vnc novnc python3-websockify \
      xdotool wmctrl xclip imagemagick \
      chromium \
      fonts-dejavu-core fonts-liberation fonts-noto-core fonts-noto-color-emoji && \
    apt-get clean && rm -rf /var/lib/apt/lists/* /usr/share/doc/* /usr/share/man/* && \
    find / -xdev -perm /6000 -type f -exec chmod a-s {} + && \
    (id app >/dev/null 2>&1 || useradd -m -u 1000 -s /bin/bash app) && \
    mkdir -p /workspace && chown 1000:1000 /workspace
COPY --chmod=0755 images/sandbox-desktop/gregale-desktop /usr/local/bin/gregale-desktop
COPY --chmod=0644 images/sandbox-desktop/index.html /usr/share/novnc/index.html
COPY --chmod=0644 images/sandbox-desktop/chromium-flags /etc/chromium.d/gregale
COPY --chmod=0644 images/sandbox-desktop/display.sh /etc/profile.d/gregale-display.sh
COPY --chmod=0644 images/sandbox-desktop/xfwm4.xml /etc/xdg/xfce4/xfconf/xfce-perchannel-xml/xfwm4.xml
ENV DISPLAY=:0
WORKDIR /workspace
