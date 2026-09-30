#!/bin/sh
set -eu

cd "$(dirname "$0")"

if ! command -v go >/dev/null 2>&1; then
	echo "install.sh: Go is required to build ogsa: https://go.dev/dl/" >&2
	exit 1
fi

bin_dir="${PREFIX:-$HOME/.local}/bin"
mkdir -p "$bin_dir"
go build -trimpath -o "$bin_dir/ogsa" ./cmd/ogsa
echo "installed $bin_dir/ogsa"

case ":$PATH:" in
*":$bin_dir:"*) ;;
*)
	echo
	echo "$bin_dir is not on your PATH. Add this to your shell profile (e.g. ~/.zshrc):"
	echo "  export PATH=\"$bin_dir:\$PATH\""
	;;
esac
