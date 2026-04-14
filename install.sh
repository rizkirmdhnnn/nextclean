#!/bin/sh
set -e

REPO="rizkirmdhnnn/nextclean"
INSTALL_DIR="/usr/local/bin"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    arm64)   ARCH="arm64" ;;
    *)       echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
    linux)  OS="linux" ;;
    darwin) OS="darwin" ;;
    *)      echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Get latest release tag
TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)

if [ -z "$TAG" ]; then
    echo "Error: could not fetch latest release."
    echo ""
    echo "No releases found yet. Install with Go instead:"
    echo "  go install github.com/${REPO}@latest"
    exit 1
fi

URL="https://github.com/${REPO}/releases/download/${TAG}/nextclean-${OS}-${ARCH}"

echo "Downloading nextclean ${TAG} (${OS}/${ARCH})..."
curl -fsSL -o /tmp/nextclean "$URL"
chmod +x /tmp/nextclean

if [ -w "$INSTALL_DIR" ]; then
    mv /tmp/nextclean "$INSTALL_DIR/nextclean"
else
    echo "Installing to ${INSTALL_DIR} (requires sudo)..."
    sudo mv /tmp/nextclean "$INSTALL_DIR/nextclean"
fi

echo "nextclean ${TAG} installed to ${INSTALL_DIR}/nextclean"
