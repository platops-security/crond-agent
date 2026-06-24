#!/bin/sh
# Postinstall for rpm/deb. crond-agent is a wrapper around cron commands,
# not a daemon — there's no service to enable. This script just helps the
# operator confirm a working install and points them at the cron.d
# example shipped alongside the binary.
set -e

if command -v crond-agent >/dev/null 2>&1; then
  echo
  echo "crond-agent installed:"
  crond-agent version || true
  echo
  echo "Next steps:"
  echo "  1. Edit /etc/crond-agent/config.yaml and fill in ping_key + api_url"
  echo "     (or set CROND_PING_KEY / CROND_API_URL env vars in your cron lines)."
  echo "  2. Wrap your cron jobs — see /usr/share/doc/crond-agent/cron.d.example"
  echo "     (or the project README) for a one-liner."
  echo
fi
