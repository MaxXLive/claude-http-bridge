ARG BUILD_FROM
FROM ${BUILD_FROM}

ARG BUILD_ARCH
# Bridge release the add-on runs. The add-on has its own version, so it can change without a bridge release.
ARG BRIDGE_VERSION=2.0.1
# Claude Code release channel or exact version, updated by rebuilding the add-on.
ARG CLAUDE_VERSION=stable

RUN set -eux; \
    case "${BUILD_ARCH}" in \
      amd64) arch=amd64 ;; \
      aarch64) arch=arm64 ;; \
      *) echo "unsupported architecture ${BUILD_ARCH}" >&2; exit 1 ;; \
    esac; \
    name="claude-bridge-linux-${arch}"; \
    base="https://github.com/MaxXLive/claude-http-bridge/releases/download/v${BRIDGE_VERSION}"; \
    cd /tmp; \
    curl -fsSLO "${base}/${name}"; \
    curl -fsSL "${base}/SHA256SUMS.txt" | grep " ${name}\$" | sha256sum -c -; \
    install -m 755 "${name}" /usr/local/bin/claude-bridge; \
    rm "${name}"; \
    curl -fsSL https://claude.ai/install.sh | bash -s "${CLAUDE_VERSION}"; \
    cp -L /root/.local/bin/claude /usr/local/bin/claude; \
    rm -rf /root/.local /root/.claude /root/.claude.json; \
    claude --version; \
    curl -fsSL https://pkgs.tailscale.com/stable/debian/bookworm.noarmor.gpg \
      -o /usr/share/keyrings/tailscale-archive-keyring.gpg; \
    curl -fsSL https://pkgs.tailscale.com/stable/debian/bookworm.tailscale-keyring.list \
      -o /etc/apt/sources.list.d/tailscale.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends tailscale; \
    rm -rf /var/lib/apt/lists/*; \
    tailscale version

COPY run.sh /
RUN chmod a+x /run.sh

CMD ["/run.sh"]
