#!/bin/sh
# Install a signed, checksum-verified release without sudo.
set -eu

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

# download only accepts HTTPS, including redirects from GitHub to its asset host.
download() {
  curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
    --connect-timeout 10 --max-time 300 --retry 2 --output "$2" "$1"
}

# write_release_signers writes the YubiKeys trusted to sign releases. It must
# match .github/allowed_signers; the installer tests enforce this.
write_release_signers() {
  cat > "$1" <<'SIGNERS'
sliplane-release namespaces="git,sliplane-release" sk-ssh-ed25519@openssh.com AAAAGnNrLXNzaC1lZDI1NTE5QG9wZW5zc2guY29tAAAAIEMxU+DK5mRMKnlZCp0U4+UzaJK3KS/FOkbi9ZnKtXQzAAAAFHNzaDpzbGlwbGFuZS1yZWxlYXNl yubikey-main
sliplane-release namespaces="git,sliplane-release" sk-ssh-ed25519@openssh.com AAAAGnNrLXNzaC1lZDI1NTE5QG9wZW5zc2guY29tAAAAIJ0aJwmT4kCuEjNUVoDqluxHf2AVT9gUX0KbqqG/Ni85AAAAFHNzaDpzbGlwbGFuZS1yZWxlYXNl yubikey-backup
SIGNERS
}

executable_version() {
  version_output=$(SLIPLANE_NO_AUTO_UPDATE=1 "$1" version -o text </dev/null 2>/dev/null) || return 1
  printf '%s\n' "$version_output" | awk '
    NF == 4 && $1 == "sliplane" && $3 == "(commit" && $4 ~ /\)$/ { version=$2; count++ }
    END { if (NR != 1 || count != 1) exit 1; print version }
  '
}

# find_shell_profile sets the profile the installer manages and the lines it adds there.
find_shell_profile() {
  quoted_directory=$(printf '%s' "$install_directory" | sed "s/'/'\\\\''/g")
  path_line="export PATH='$quoted_directory':\"\$PATH\""
  config_file=
  completion_line=
  case "${SHELL:-}" in
    */zsh)
      config_file=${ZDOTDIR:-$HOME}/.zshrc
      # Completion needs compinit, which a plain zsh profile does not load.
      completion_line='if command -v sliplane >/dev/null; then command -v compdef >/dev/null || { autoload -Uz compinit && compinit -i; }; eval "$(sliplane completion zsh)"; fi'
      ;;
    */bash)
      config_file=$HOME/.bashrc
      if [ ! -f "$config_file" ] && [ -f "$HOME/.bash_profile" ]; then
        config_file=$HOME/.bash_profile
      fi
      # Completion needs the bash-completion package.
      completion_line='if command -v sliplane >/dev/null && declare -F _get_comp_words_by_ref >/dev/null; then eval "$(sliplane completion bash)"; fi'
      ;;
  esac
}

# configure_path offers shell setup without reading answers from the piped script.
configure_path() {
  if [ "$setup_path" = false ]; then
    case ":$PATH:" in
      *":$install_directory:"*) return ;;
    esac
  fi

  if [ -n "$config_file" ] && [ -f "$config_file" ] && grep -Fqx "$path_line" "$config_file"; then
    if [ "$setup_path" = false ]; then
      printf '\nTo use Sliplane in this terminal:\n\n  %s\n' "$path_line"
    fi
    return
  fi

  response=n
  if [ -n "$config_file" ]; then
    if [ "$setup_path" = true ]; then
      response=y
    elif [ "$non_interactive" = false ] && [ -t 1 ]; then
      printf '\nAdd Sliplane to PATH and set up shell completion in %s? (Y/n) ' "$config_file"
      if ! read -r response 2>/dev/null < /dev/tty; then
        response=n
      fi
    fi
  fi
  case "$response" in
    y|Y|yes|Yes|'')
      if printf '\n# Added by the Sliplane installer\n%s\n' "$path_line" >> "$config_file"; then
        if [ "$setup_path" = false ]; then
          printf '\nTo use Sliplane in this terminal:\n\n  %s\n' "$path_line"
        fi
        return
      fi
      printf '\nCould not update %s; Sliplane is installed.\n' "$config_file" >&2
      ;;
  esac
  printf '\nAdd the installation directory to your shell PATH. For bash/zsh, add this line to your shell profile:\n\n  %s\n' "$path_line"
  printf '\nRun the line in this terminal too to use Sliplane immediately.\n'
}

# configure_completion loads shell completion wherever the installer added
# Sliplane to PATH. The binary generates it, so it stays current after updates.
configure_completion() {
  if [ -z "$completion_line" ] || [ ! -f "$config_file" ] || ! grep -Fqx "$path_line" "$config_file" \
    || grep -Fqx "$completion_line" "$config_file"; then
    return
  fi
  printf '%s\n' "$completion_line" >> "$config_file" \
    || printf '\nCould not set up shell completion in %s.\n' "$config_file" >&2
}

# install_sliplane keeps the full operation in a function so a truncated piped script cannot run it.
install_sliplane() {
  version=
  non_interactive=false
  setup_path=false
  for argument in "$@"; do
    case "$argument" in
      --non-interactive) non_interactive=true ;;
      --setup-path) setup_path=true ;;
      --help|-h)
        printf 'Usage: sh install.sh [VERSION] [--non-interactive] [--setup-path]\n'
        return
        ;;
      -*) fail "Unknown option: $argument. Use --help for usage." ;;
      *)
        [ -z "$version" ] || fail 'Specify only one version. Use --help for usage.'
        version=$argument
        ;;
    esac
  done
  for dependency in curl tar awk; do
    command -v "$dependency" >/dev/null 2>&1 || fail "Install $dependency and try again."
  done
  command -v ssh-keygen >/dev/null 2>&1 || fail 'Install OpenSSH 8.2 or newer (ssh-keygen) to verify the release signature and try again.'

  case "$(uname -s)" in
    Darwin) platform=darwin ;;
    Linux) platform=linux ;;
    *) fail 'Supported systems are macOS and Linux (including WSL).' ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) architecture=amd64 ;;
    arm64|aarch64) architecture=arm64 ;;
    *) fail 'Supported architectures are x86_64 and ARM64.' ;;
  esac

  checksum_tool=sha256sum
  if ! command -v sha256sum >/dev/null 2>&1; then
    checksum_tool=shasum
    command -v shasum >/dev/null 2>&1 || fail 'Install sha256sum or shasum and try again.'
  fi

  releases=https://github.com/sliplane/cli/releases
  version=${version:-latest}
  if [ "$version" = latest ]; then
    release_url=$(curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
      --connect-timeout 10 --max-time 30 --retry 2 --output /dev/null --write-out '%{url_effective}' "$releases/latest") \
      || fail 'Could not find the latest release. Check your connection and try again.'
    case "$release_url" in
      "$releases/tag/"*) version=${release_url##*/} ;;
      *) fail 'GitHub did not return a published release.' ;;
    esac
  fi
  version=${version#v}
  if ! printf '%s\n' "$version" | awk '/^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$/ { valid=1 } END { exit !valid }'; then
    fail 'Use a version such as 0.1.0 or v0.1.0-rc.1.'
  fi

  install_directory=${SLIPLANE_INSTALL_DIR:-"$HOME/.sliplane/bin"}
  case "$install_directory" in
    /*) ;;
    *) fail 'SLIPLANE_INSTALL_DIR must be an absolute path.' ;;
  esac
  destination=$install_directory/sliplane
  if [ -L "$destination" ] || [ -d "$destination" ]; then
    fail "$destination is a symlink or directory. Choose another SLIPLANE_INSTALL_DIR."
  fi

  temporary_directory=$(mktemp -d)
  staged_binary=
  trap 'rm -rf "$temporary_directory"; if [ -n "$staged_binary" ]; then rm -f "$staged_binary"; fi' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  archive=sliplane_${version}_${platform}_${architecture}.tar.gz
  release=$releases/download/v$version
  printf 'Downloading Sliplane %s…\n' "$version"
  download "$release/$archive" "$temporary_directory/$archive" || fail "Could not download $archive."
  download "$release/checksums.txt" "$temporary_directory/checksums.txt" || fail 'Could not download release checksums.'
  download "$release/checksums.txt.sig" "$temporary_directory/checksums.txt.sig" \
    || fail 'Could not download the release signature. Only releases signed by a Sliplane maintainer can be installed.'
  write_release_signers "$temporary_directory/allowed_signers"
  ssh-keygen -Y verify -f "$temporary_directory/allowed_signers" -I sliplane-release -n sliplane-release \
    -s "$temporary_directory/checksums.txt.sig" < "$temporary_directory/checksums.txt" >/dev/null 2>&1 \
    || fail 'Release signature verification failed (this requires OpenSSH 8.2 or newer). The existing installation was not changed.'
  expected=$(awk -v file="$archive" '$2 == file { count++; hash=$1 } END { if (count != 1) exit 1; print hash }' "$temporary_directory/checksums.txt") \
    || fail "The release must contain exactly one checksum for $archive."
  if [ "$checksum_tool" = sha256sum ]; then
    actual=$(sha256sum "$temporary_directory/$archive")
  else
    actual=$(shasum -a 256 "$temporary_directory/$archive")
  fi
  actual=${actual%% *}
  [ "$actual" = "$expected" ] || fail 'Checksum verification failed. The existing installation was not changed.'
  tar -xzf "$temporary_directory/$archive" -C "$temporary_directory" sliplane \
    || fail 'Could not extract the Sliplane executable.'
  if [ ! -f "$temporary_directory/sliplane" ] || [ -L "$temporary_directory/sliplane" ]; then
    fail 'The release does not contain a regular Sliplane executable.'
  fi

  mkdir -p "$install_directory" || fail "Cannot create $install_directory. Choose a writable SLIPLANE_INSTALL_DIR."
  staged_binary=$(mktemp "$install_directory/.sliplane.XXXXXX")
  cp "$temporary_directory/sliplane" "$staged_binary"
  chmod 755 "$staged_binary"

  verified_version=$(executable_version "$staged_binary") \
    || fail 'The downloaded Sliplane executable could not run or returned invalid version information. The existing installation was not changed.'
  [ "$verified_version" = "$version" ] \
    || fail "The downloaded executable reports version $verified_version instead of $version. The existing installation was not changed."

  first_install=true
  if [ -f "$destination" ]; then
    first_install=false
  fi
  if [ -x "$destination" ] && cmp -s "$staged_binary" "$destination"; then
    printf 'Already up to date: Sliplane %s\n' "$version"
  else
    previous_version=
    if [ "$first_install" = false ]; then
      previous_version=$(executable_version "$destination" || true)
    fi
    mv -f "$staged_binary" "$destination" || fail "Cannot install to $destination."
    staged_binary=
    if [ "$first_install" = true ]; then
      printf 'Installed Sliplane %s\n' "$version"
    elif [ -n "$previous_version" ] && [ "$previous_version" != "$version" ]; then
      printf 'Updated Sliplane %s → %s\n' "$previous_version" "$version"
    else
      printf 'Reinstalled Sliplane %s\n' "$version"
    fi
  fi
  find_shell_profile
  configure_path
  configure_completion
  current=$(command -v sliplane || true)
  if [ "$setup_path" = false ] && [ -n "$current" ] && [ "$current" != "$destination" ]; then
    printf '\nYour PATH currently selects %s. Put %s first to use this installation.\n' "$current" "$install_directory"
  fi
  if [ "$first_install" = true ]; then
    printf '\nGet started: sliplane auth login\n'
  fi
}

install_sliplane "$@"
