#!/bin/sh
set -e

# Default API URL if not provided
API_URL=${VITE_API_URL:-"http://localhost:8080"}

# Write runtime config.js with the actual API URL
cat <<EOF >/app/dist/config.js
// Runtime configuration injected by entrypoint
window.APP_CONFIG = {
  API_URL: '$API_URL'
};
EOF

echo "Configured API_URL: $API_URL"

# Start the serve command
exec "$@"