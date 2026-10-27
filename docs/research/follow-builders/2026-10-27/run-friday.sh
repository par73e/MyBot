#!/bin/sh
# Server-only transport/runtime adapter. Does not schedule or deliver messages.
set -eu
skill_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
exec env HTTPS_PROXY=http://127.0.0.1:17890 HTTP_PROXY=http://127.0.0.1:17890 \
  /opt/mybot/tools/node-v22.23.4-linux-x64/bin/node --use-env-proxy \
  --require "$skill_dir/scripts/github-transport.cjs" \
  "$skill_dir/scripts/prepare-digest.js"
