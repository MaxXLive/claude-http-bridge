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

# Tailscale runs as a device of its own, in userspace mode so the add-on needs no extra privileges.
# The login and the funnel setting persist in /data/tailscale.
start_tailscale() {
    local socket=/var/run/tailscale/tailscaled.sock
    mkdir -p /data/tailscale "$(dirname "${socket}")"
    tailscaled --tun=userspace-networking --statedir=/data/tailscale --socket="${socket}" > /dev/null 2>&1 &
    for _ in $(seq 30); do
        [[ -S "${socket}" ]] && break
        sleep 1
    done

    local up=(tailscale up --reset --hostname "$(option tailscale_hostname)")
    local authkey="$(option tailscale_authkey)"
    if [[ -n "${authkey}" ]]; then
        up+=(--auth-key "${authkey}")
    fi
    # Without an auth key this prints a login link and waits until it has been opened.
    if ! "${up[@]}"; then
        bashio::log.error "Tailscale login failed"
        return
    fi
    # In userspace mode the tailnet only reaches the bridge through serve (tailnet) or funnel (internet).
    tailscale serve reset
    local reach
    if [[ "$(option tailscale_funnel)" == "true" ]]; then
        # The first time this prints a link to enable Funnel for the tailnet and waits for it.
        tailscale funnel --bg 8787
        reach="from the internet"
    else
        tailscale serve --bg 8787
        reach="only inside the tailnet"
    fi
    # Right after a rename the status still has the old name, the serve config already the new one.
    local host="$(tailscale serve status --json | jq -r '.Web // {} | keys[0] // empty')"
    bashio::log.info "Tailscale: https://${host%:443} (${reach}, bridge.url for the plugins)"
}

if [[ "$(option tailscale)" == "true" ]]; then
    start_tailscale &
fi

exec claude-bridge serve "${args[@]}"
