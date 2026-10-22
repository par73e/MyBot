#!/bin/sh
# Run as root, with the extracted deployment bundle as the first argument.
set -eu
bundle=$(realpath "${1:?deployment bundle directory required}")
release=/opt/mybot/releases/v1.39.10-heartbeat.1
test "$(id -u)" -eq 0
test "$(uname -m)" = x86_64
cd "$bundle"
sha256sum -c SHA256SUMS
if ! getent passwd mybot >/dev/null; then
  useradd --system --user-group --home-dir /var/lib/mybot --shell /usr/sbin/nologin mybot
fi
install -d -o root -g root -m 0755 /opt/mybot /opt/mybot/releases "$release"
install -d -o mybot -g mybot -m 0700 /var/lib/mybot /var/lib/mybot/reasonix /var/lib/mybot/workspace
if test -e "$release/reasonix"; then
  cmp "$bundle/reasonix" "$release/reasonix"
else
  install -o root -g root -m 0755 reasonix "$release/reasonix"
fi
install -o root -g root -m 0644 test-headless-heartbeat.py HEADLESS_HEARTBEAT.zh-CN.md UPSTREAM_BASE.md heartbeat-v1.39.10.patch SHA256SUMS "$release/"
if test -e /opt/mybot/current && ! test -L /opt/mybot/current; then
  echo '/opt/mybot/current exists and is not a symlink; refusing to replace' >&2
  exit 1
fi
ln -sfn "$release" /opt/mybot/current
if ! test -e /var/lib/mybot/reasonix/config.toml; then
  install -o mybot -g mybot -m 0600 config.initial.toml /var/lib/mybot/reasonix/config.toml
fi
if ! test -e /var/lib/mybot/reasonix/heartbeat-cli.json; then
  printf '{"schemaVersion":1,"tasks":[]}\n' > /var/lib/mybot/reasonix/heartbeat-cli.json
  chown mybot:mybot /var/lib/mybot/reasonix/heartbeat-cli.json
  chmod 0600 /var/lib/mybot/reasonix/heartbeat-cli.json
fi
if test -e /usr/local/bin/mybot; then
  cmp mybot /usr/local/bin/mybot
else
  install -o root -g root -m 0755 mybot /usr/local/bin/mybot
fi
if test -e /etc/systemd/system/mybot-heartbeat.service; then
  cmp mybot-heartbeat.service /etc/systemd/system/mybot-heartbeat.service
else
  install -o root -g root -m 0644 mybot-heartbeat.service /etc/systemd/system/mybot-heartbeat.service
fi
systemd-analyze verify /etc/systemd/system/mybot-heartbeat.service
systemctl daemon-reload
systemctl enable --now mybot-heartbeat.service
systemctl is-active mybot-heartbeat.service
/usr/local/bin/mybot --version
