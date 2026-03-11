#!/usr/bin/env bash
# Install pssh to /usr/local/bin and add shell completions

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALL_DIR="/usr/local/bin"

echo "Installing pssh..."

# Symlink to PATH
if [[ -L "${INSTALL_DIR}/pssh" ]] || [[ -f "${INSTALL_DIR}/pssh" ]]; then
    echo "  Removing existing ${INSTALL_DIR}/pssh"
    rm -f "${INSTALL_DIR}/pssh"
fi

ln -s "${SCRIPT_DIR}/pssh" "${INSTALL_DIR}/pssh"
echo "  Linked pssh → ${INSTALL_DIR}/pssh"

# Detect shell config file
SHELL_RC=""
if [[ "$SHELL" == *"zsh"* ]]; then
    SHELL_RC="$HOME/.zshrc"
elif [[ "$SHELL" == *"bash"* ]]; then
    SHELL_RC="$HOME/.bashrc"
fi

# Add convenience aliases if not already present
if [[ -n "$SHELL_RC" ]] && ! grep -q '# pssh aliases' "$SHELL_RC" 2>/dev/null; then
    cat >> "$SHELL_RC" <<'EOF'

# pssh aliases — persistent SSH sessions
alias pl='pssh list'
alias pa='pssh attach'
alias pk='pssh kill'
alias ps_status='pssh status'
EOF
    echo "  Added aliases to ${SHELL_RC}"
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
