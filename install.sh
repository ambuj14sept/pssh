#!/usr/bin/env bash
# Install or uninstall pssh
# Usage: ./install.sh           # install
#        ./install.sh --uninstall  # uninstall

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALL_DIR="/usr/local/bin"

# Detect shell config file
detect_shell_rc() {
    if [[ "$SHELL" == *"zsh"* ]]; then
        echo "$HOME/.zshrc"
    elif [[ "$SHELL" == *"bash"* ]]; then
        echo "$HOME/.bashrc"
    else
        echo ""
    fi
}

do_uninstall() {
    echo "Uninstalling pssh..."

    # Remove symlink/binary
    if [[ -L "${INSTALL_DIR}/pssh" ]] || [[ -f "${INSTALL_DIR}/pssh" ]]; then
        rm -f "${INSTALL_DIR}/pssh"
        echo "  Removed ${INSTALL_DIR}/pssh"
    else
        echo "  ${INSTALL_DIR}/pssh not found (already removed?)"
    fi

    # Remove aliases from shell rc
    local shell_rc
    shell_rc=$(detect_shell_rc)
    if [[ -n "$shell_rc" ]] && [[ -f "$shell_rc" ]]; then
        if grep -q '# pssh aliases' "$shell_rc" 2>/dev/null; then
            # Remove the alias block (the comment + 4 alias lines + preceding blank line)
            sed -i '/^$/N;/\n# pssh aliases/,/^alias ps_status=/d' "$shell_rc"
            echo "  Removed aliases from ${shell_rc}"
        else
            echo "  No aliases found in ${shell_rc}"
        fi
    fi

    # Clean up session tracking dir
    if [[ -d "${HOME}/.pssh" ]]; then
        rm -rf "${HOME}/.pssh"
        echo "  Removed ${HOME}/.pssh"
    fi

    echo ""
    echo "Done! pssh has been uninstalled."
    echo "Run 'source ${shell_rc}' or open a new terminal to apply changes."
}

do_install() {
    echo "Installing pssh..."

    # Build first
    if [[ ! -f "${SCRIPT_DIR}/bin/pssh" ]]; then
        echo "  Building pssh..."
        cd "$SCRIPT_DIR"
        make build
    fi

    # Copy binary to PATH (not symlink, since it's a compiled binary)
    if [[ -L "${INSTALL_DIR}/pssh" ]] || [[ -f "${INSTALL_DIR}/pssh" ]]; then
        echo "  Removing existing ${INSTALL_DIR}/pssh"
        rm -f "${INSTALL_DIR}/pssh"
    fi

    cp "${SCRIPT_DIR}/bin/pssh" "${INSTALL_DIR}/pssh"
    chmod +x "${INSTALL_DIR}/pssh"
    echo "  Installed pssh → ${INSTALL_DIR}/pssh"

    # Add convenience aliases if not already present
    local shell_rc
    shell_rc=$(detect_shell_rc)
    if [[ -n "$shell_rc" ]] && ! grep -q '# pssh aliases' "$shell_rc" 2>/dev/null; then
        cat >> "$shell_rc" <<'EOF'

# pssh aliases — persistent SSH sessions
alias pl='pssh list'
alias pa='pssh attach'
alias pk='pssh kill'
alias ps_status='pssh status'
EOF
        echo "  Added aliases to ${shell_rc}"
        echo "    pl  → pssh list"
        echo "    pa  → pssh attach"
        echo "    pk  → pssh kill"
        echo "    ps_status → pssh status"
    fi

    echo ""
    echo "Done! Run 'pssh --help' to get started."
    echo ""
    echo "Quick start:"
    echo "  pssh user@server                  # persistent shell"
    echo "  pssh user@server -- ./my-binary   # run binary persistently"
    echo "  pssh list user@server             # see active sessions"
}

case "${1:-}" in
    --uninstall|-u|uninstall)
        do_uninstall
        ;;
    --help|-h)
        echo "Usage: ./install.sh              # install pssh"
        echo "       ./install.sh --uninstall   # uninstall pssh"
        ;;
    *)
        do_install
        ;;
esac
