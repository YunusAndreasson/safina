#!/bin/sh
# Installs safina from its GitHub release.
#
#   curl -fsSL https://safina.run/install.sh | sh
#
# Linux on x86-64 or ARM64 with glibc 2.34 or newer, or macOS 13 or newer
# on Apple silicon or Intel. It downloads the release archive and its checksums, verifies the archive's SHA-256, and puts
# the binary in ~/.local/bin. Nothing else on the machine changes, and
# nothing runs as root.
#
#   SAFINA_INSTALL_DIR  where the binary goes (default: ~/.local/bin)
#   SAFINA_VERSION      a release such as 0.1.0 (default: the latest)
#
# Source: https://github.com/YunusAndreasson/safina (site/public/install.sh)
set -eu

repo="YunusAndreasson/safina"

say() { printf '%s\n' "$*" >&2; }
die() {
	say "safina install: $*"
	exit 1
}

# Every step runs from main, called on the last line, so a download cut off
# part way through runs nothing.
main() {
	os=$(uname -s)
	case "$os" in
	Linux) platform=linux ;;
	Darwin) platform=darwin ;;
	*) die "no binary for $os. Build from source: https://github.com/$repo" ;;
	esac

	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "no binary for $(uname -m). Build from source: https://github.com/$repo" ;;
	esac

	if [ "$platform" = linux ]; then
		# The release build refuses a binary that needs anything newer than 2.34.
		libc=$(getconf GNU_LIBC_VERSION 2>/dev/null || true)
		case "$libc" in
		"glibc "*) ;;
		*) die "safina's binaries need glibc, and this system doesn't report one (musl, as on Alpine?). Build from source: https://github.com/$repo" ;;
		esac
		have=${libc#glibc }
		if [ "$(printf '%s\n2.34\n' "$have" | sort -t. -k1,1n -k2,2n | head -n 1)" != 2.34 ]; then
			die "safina's binaries need glibc 2.34 or newer; this system has $have. Build from source: https://github.com/$repo"
		fi
	else
		# The release build targets macOS 13.
		have=$(sw_vers -productVersion 2>/dev/null || echo 0)
		if [ "${have%%.*}" -lt 13 ] 2>/dev/null; then
			die "safina's binaries need macOS 13 or newer; this Mac has $have. Build from source: https://github.com/$repo"
		fi
	fi

	if command -v curl >/dev/null 2>&1; then
		fetch() { curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1"; }
	elif command -v wget >/dev/null 2>&1; then
		fetch() { wget -q --https-only -O "$2" "$1"; }
	else
		die "needs curl or wget"
	fi
	if command -v sha256sum >/dev/null 2>&1; then
		sum() { sha256sum "$1" | cut -d ' ' -f 1; }
	elif command -v shasum >/dev/null 2>&1; then
		sum() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
	else
		die "needs sha256sum or shasum to verify the download"
	fi

	version=${SAFINA_VERSION:-}
	version=${version#v}
	if [ -n "$version" ]; then
		base="https://github.com/$repo/releases/download/v$version"
	else
		base="https://github.com/$repo/releases/latest/download"
	fi
	file="safina_${platform}_$arch.tar.gz"
	dir=${SAFINA_INSTALL_DIR:-$HOME/.local/bin}

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	trap 'exit 130' INT TERM

	say "Downloading $file from ${version:+v$version of }$repo"
	fetch "$base/$file" "$tmp/$file" || die "couldn't download $base/$file"
	fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "couldn't download $base/checksums.txt"

	want=$(awk -v f="$file" '$2 == f || $2 == "*" f { print $1 }' "$tmp/checksums.txt")
	[ -n "$want" ] || die "checksums.txt lists no $file"
	[ "$(sum "$tmp/$file")" = "$want" ] || die "$file doesn't match its SHA-256 in checksums.txt; nothing was installed"

	tar -xzf "$tmp/$file" -C "$tmp" safina || die "couldn't unpack $file"
	mkdir -p "$dir"
	# Replaced in one rename, so a running safina keeps its binary.
	cp "$tmp/safina" "$dir/.safina.new"
	chmod 755 "$dir/.safina.new"
	mv -f "$dir/.safina.new" "$dir/safina"

	say "Installed $("$dir/safina" --version 2>/dev/null || echo safina) to $dir/safina"
	case ":$PATH:" in
	*":$dir:"*) say "Run: safina" ;;
	*) say "$dir isn't on your PATH. Add it, then run safina:  export PATH=\"$dir:\$PATH\"" ;;
	esac
}

main "$@"
