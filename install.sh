#!/bin/sh
# ─────────────────────────────────────────────────────────────────────────────
#  Chandra — Install Script
#  An autonomous LLM agent that thinks, plans, and acts.
#
#  Usage:
#    curl -fsSL https://raw.githubusercontent.com/jrimmer/chandra/main/install.sh | sh
#
#  What this script does:
#    1. Detects your OS and architecture
#    2. Installs prerequisites (Go, SQLite, GCC)
#    3. Builds Chandra from source
#    4. Installs chandrad (daemon) and chandra (CLI) to /usr/local/bin
#    5. Runs `chandra init` to set up your configuration
#
#  Supports: Linux (Debian/Ubuntu, RHEL/Fedora, Arch, Alpine), macOS
#  Requires: root/sudo for package installation
# ─────────────────────────────────────────────────────────────────────────────

set -e

# ── Colors & formatting ──────────────────────────────────────────────────────

BOLD='\033[1m'
DIM='\033[2m'
CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
RESET='\033[0m'

CHANDRA_REPO="github.com/jrimmer/chandra"
CHANDRA_GIT="https://github.com/jrimmer/chandra.git"
GO_MIN_VERSION="1.23"
GO_INSTALL_VERSION="1.24.1"
BUILD_TAGS="sqlite_fts5"
INSTALL_DIR="/usr/local/bin"

# ── Helpers ──────────────────────────────────────────────────────────────────

banner() {
    printf "\n${CYAN}${BOLD}"
    printf "  ┌─────────────────────────────────────────┐\n"
    printf "  │           🔮 Chandra Installer           │\n"
    printf "  │     An Autonomous LLM Agent System       │\n"
    printf "  └─────────────────────────────────────────┘\n"
    printf "${RESET}\n"
}

step() {
    printf "\n${GREEN}${BOLD}▸ %s${RESET}\n" "$1"
}

info() {
    printf "  ${DIM}%s${RESET}\n" "$1"
}

warn() {
    printf "  ${YELLOW}⚠ %s${RESET}\n" "$1"
}

fail() {
    printf "\n  ${RED}✗ %s${RESET}\n\n" "$1"
    exit 1
}

ok() {
    printf "  ${GREEN}✓ %s${RESET}\n" "$1"
}

command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# ── Detect OS & Architecture ────────────────────────────────────────────────

detect_platform() {
    step "Detecting platform"

    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    ARCH="$(uname -m)"

    case "$ARCH" in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        armv7l) ARCH="armv6l" ;;
        *) fail "Unsupported architecture: $ARCH" ;;
    esac

    case "$OS" in
        linux)
            if [ -f /etc/os-release ]; then
                . /etc/os-release
                DISTRO="$ID"
                DISTRO_FAMILY="$ID_LIKE"
            elif [ -f /etc/alpine-release ]; then
                DISTRO="alpine"
            else
                DISTRO="unknown"
            fi
            ;;
        darwin)
            DISTRO="macos"
            ;;
        *)
            fail "Unsupported OS: $OS"
            ;;
    esac

    ok "OS: $OS ($DISTRO) | Arch: $ARCH"
}

# ── Privilege check ─────────────────────────────────────────────────────────

check_privileges() {
    step "Checking privileges"

    if [ "$(id -u)" -eq 0 ]; then
        SUDO=""
        ok "Running as root"
    elif command_exists sudo; then
        SUDO="sudo"
        ok "sudo available — will prompt for password when needed"
    else
        fail "This script requires root or sudo to install packages and binaries."
    fi
}

# ── Package manager detection ───────────────────────────────────────────────

detect_package_manager() {
    if command_exists apt-get; then
        PKG_MGR="apt"
    elif command_exists dnf; then
        PKG_MGR="dnf"
    elif command_exists yum; then
        PKG_MGR="yum"
    elif command_exists pacman; then
        PKG_MGR="pacman"
    elif command_exists apk; then
        PKG_MGR="apk"
    elif command_exists brew; then
        PKG_MGR="brew"
    else
        fail "No supported package manager found (apt, dnf, yum, pacman, apk, brew)"
    fi
    info "Package manager: $PKG_MGR"
}

# ── Install prerequisites ───────────────────────────────────────────────────

install_prerequisites() {
    step "Installing prerequisites (GCC, SQLite, Git, Make)"

    case "$PKG_MGR" in
        apt)
            $SUDO apt-get update -qq
            $SUDO apt-get install -y -qq gcc make git libsqlite3-dev >/dev/null 2>&1
            ;;
        dnf)
            $SUDO dnf install -y -q gcc make git sqlite-devel >/dev/null 2>&1
            ;;
        yum)
            $SUDO yum install -y -q gcc make git sqlite-devel >/dev/null 2>&1
            ;;
        pacman)
            $SUDO pacman -Sy --noconfirm --quiet gcc make git sqlite >/dev/null 2>&1
            ;;
        apk)
            $SUDO apk add --quiet gcc musl-dev make git sqlite-dev >/dev/null 2>&1
            ;;
        brew)
            brew install --quiet sqlite git 2>/dev/null
            ;;
    esac

    # Verify essentials
    command_exists gcc || fail "GCC installation failed"
    command_exists git || fail "Git installation failed"
    command_exists make || fail "Make installation failed"

    ok "Prerequisites installed"
}

# ── Install Go ──────────────────────────────────────────────────────────────

version_ge() {
    # Returns 0 if $1 >= $2 (semver-ish)
    printf '%s\n%s\n' "$2" "$1" | sort -V -C
}

install_go() {
    step "Checking Go installation"

    if command_exists go; then
        CURRENT_GO=$(go version | grep -oE 'go[0-9]+\.[0-9]+(\.[0-9]+)?' | sed 's/go//')
        if version_ge "$CURRENT_GO" "$GO_MIN_VERSION"; then
            ok "Go $CURRENT_GO found (>= $GO_MIN_VERSION required)"
            return
        else
            warn "Go $CURRENT_GO found but >= $GO_MIN_VERSION required — upgrading"
        fi
    else
        info "Go not found — installing Go $GO_INSTALL_VERSION"
    fi

    step "Installing Go $GO_INSTALL_VERSION"

    GO_TARBALL="go${GO_INSTALL_VERSION}.${OS}-${ARCH}.tar.gz"
    GO_URL="https://go.dev/dl/${GO_TARBALL}"

    info "Downloading from $GO_URL"
    TMPDIR=$(mktemp -d)
    curl -fsSL "$GO_URL" -o "$TMPDIR/$GO_TARBALL" || fail "Failed to download Go"

    info "Installing to /usr/local/go"
    $SUDO rm -rf /usr/local/go
    $SUDO tar -C /usr/local -xzf "$TMPDIR/$GO_TARBALL"
    rm -rf "$TMPDIR"

    # Add to PATH for this session
    export PATH="/usr/local/go/bin:$PATH"

    # Ensure future sessions have Go in PATH
    if [ "$OS" = "linux" ]; then
        if ! grep -q '/usr/local/go/bin' /etc/profile.d/go.sh 2>/dev/null; then
            $SUDO sh -c 'echo "export PATH=\$PATH:/usr/local/go/bin" > /etc/profile.d/go.sh'
            info "Added Go to PATH via /etc/profile.d/go.sh"
        fi
    fi

    go version >/dev/null 2>&1 || fail "Go installation failed"
    ok "Go $(go version | grep -oE 'go[0-9]+\.[0-9]+\.[0-9]+') installed"
}

# ── Build Chandra ───────────────────────────────────────────────────────────

build_chandra() {
    step "Building Chandra from source"

    TMPDIR=$(mktemp -d)
    info "Cloning $CHANDRA_GIT"
    git clone --depth 1 "$CHANDRA_GIT" "$TMPDIR/chandra" 2>/dev/null || fail "Failed to clone repository"

    cd "$TMPDIR/chandra"

    VERSION=$(git describe --tags --always 2>/dev/null || echo "dev")
    info "Version: $VERSION"

    info "Compiling chandrad (daemon)..."
    CGO_ENABLED=1 go build -tags "$BUILD_TAGS" -ldflags "-X main.version=$VERSION" -o bin/chandrad ./cmd/chandrad \
        || fail "Failed to build chandrad"

    info "Compiling chandra (CLI)..."
    CGO_ENABLED=1 go build -tags "$BUILD_TAGS" -ldflags "-X main.version=$VERSION" -o bin/chandra ./cmd/chandra \
        || fail "Failed to build chandra CLI"

    ok "Build complete"
}

# ── Install binaries ────────────────────────────────────────────────────────

install_binaries() {
    step "Installing Chandra to $INSTALL_DIR"

    $SUDO cp bin/chandrad "$INSTALL_DIR/chandrad"
    $SUDO cp bin/chandra "$INSTALL_DIR/chandra"
    $SUDO chmod +x "$INSTALL_DIR/chandrad" "$INSTALL_DIR/chandra"

    if [ -f scripts/chandrad-config-apply.sh ]; then
        $SUDO cp scripts/chandrad-config-apply.sh "$INSTALL_DIR/chandrad-config-apply"
        $SUDO chmod +x "$INSTALL_DIR/chandrad-config-apply"
    fi

    ok "chandrad → $INSTALL_DIR/chandrad"
    ok "chandra  → $INSTALL_DIR/chandra"

    # Clean up build directory
    cd /
    rm -rf "$TMPDIR"
}

# ── Verify installation ────────────────────────────────────────────────────

verify_install() {
    step "Verifying installation"

    CHANDRAD_VERSION=$("$INSTALL_DIR/chandrad" --version 2>/dev/null || echo "unknown")
    CHANDRA_VERSION=$("$INSTALL_DIR/chandra" --version 2>/dev/null || echo "unknown")

    ok "chandrad: $CHANDRAD_VERSION"
    ok "chandra:  $CHANDRA_VERSION"
}

# ── Run init ────────────────────────────────────────────────────────────────

run_init() {
    step "Setting up Chandra"
    info "Running 'chandra init' to configure your agent..."
    printf "\n"

    "$INSTALL_DIR/chandra" init || warn "Init exited with an error — you can re-run 'chandra init' anytime"
}

# ── Done ────────────────────────────────────────────────────────────────────

done_message() {
    printf "\n${GREEN}${BOLD}"
    printf "  ┌─────────────────────────────────────────┐\n"
    printf "  │         ✅ Chandra is installed!         │\n"
    printf "  └─────────────────────────────────────────┘\n"
    printf "${RESET}\n"
    printf "  ${BOLD}Quick start:${RESET}\n"
    printf "    ${CYAN}chandrad${RESET}          Start the daemon\n"
    printf "    ${CYAN}chandra status${RESET}    Check agent status\n"
    printf "    ${CYAN}chandra init${RESET}      Re-run setup\n"
    printf "\n"
    printf "  ${BOLD}Docs:${RESET}  https://github.com/jrimmer/chandra\n"
    printf "\n"
}

# ── Main ────────────────────────────────────────────────────────────────────

main() {
    banner
    detect_platform
    check_privileges
    detect_package_manager
    install_prerequisites
    install_go
    build_chandra
    install_binaries
    verify_install
    run_init
    done_message
}

main "$@"
