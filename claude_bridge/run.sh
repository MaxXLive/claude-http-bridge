#!/usr/bin/with-contenv bashio
# Starts the bridge with the add-on options. Everything that has to survive restarts lives in /data:
# the authorized_keys written from the options, the client workspaces and the CLI's own config.
set -e

export HOME=/data
export DISABLE_AUTOUPDATER=1
keys=/data/authorized_keys

# The Supervisor writes the options here, reading them directly also works without its API.
option() {
    jq -r --arg name "$1" '.[$name] // empty' /data/options.json
}

token="$(option oauth_token)"
if [[ -n "${token}" ]]; then
    export CLAUDE_CODE_OAUTH_TOKEN="${token}"
elif [[ ! -f /data/.claude/.credentials.json ]]; then
    bashio::log.warning "No oauth_token set: create one with 'claude setup-token' and add it in the add-on configuration."
fi

# The options are the only source of keys, so removing one there revokes it.
: > "${keys}"
chmod 600 "${keys}"
while IFS= read -r line; do
    # also accept the whole line from the plugin's log: claude-bridge add-key "ed25519 ..."
    line="${line#claude-bridge add-key }"
    line="${line//\"/}"
    [[ -n "${line}" ]] || continue
    claude-bridge add-key --authorized-keys "${keys}" "${line}" || bashio::log.error "Skipped key: ${line}"
done < <(jq -r '.authorized_keys[]?' /data/options.json)

args=(
    --host 0.0.0.0
    --port 8787
    --authorized-keys "${keys}"
    --workspaces /data/workspaces
    --model "$(option model)"
    --parallel "$(option parallel)"
    --queue-timeout "$(option queue_timeout)"
    --timeout "$(option timeout)"
)
if [[ "$(option web)" == "false" ]]; then
    args+=(--no-web)
fi
domains="$(option web_domains)"
if [[ -n "${domains}" ]]; then
    args+=(--web-domains "${domains}")
fi

exec claude-bridge serve "${args[@]}"
