#!/bin/sh
# Maintainer release tasks that need a YubiKey. See docs/releasing.md.
#   enroll NAME        create a release key on the connected YubiKey and trust it
#   check NAME         sign a test file and verify it as install.sh does
#   tag VERSION NAME   create and push a signed release tag
#   sign VERSION NAME  rebuild the draft release, compare, sign, and publish it
set -eu

repository=sliplane/cli
goreleaser_version=2.15.1

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

usage() {
  fail 'Usage: scripts/release.sh enroll NAME | check NAME | tag VERSION NAME | sign VERSION NAME'
}

# macOS's own ssh-keygen cannot use YubiKeys; Homebrew's OpenSSH can.
ssh_keygen=/opt/homebrew/opt/openssh/bin/ssh-keygen
[ -x "$ssh_keygen" ] || ssh_keygen=ssh-keygen
root=$(git rev-parse --show-toplevel)
signers=$root/.github/allowed_signers
installer=$root/scripts/install.sh
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# use_key sets key to the key file for the YubiKey named $1.
use_key() {
  printf '%s\n' "$1" | grep -Eq '^[A-Za-z0-9_-]+$' || fail 'Key names may contain letters, digits, - and _.'
  key=$HOME/.ssh/sliplane-release-$1
}

enroll() {
  use_key "$1"
  ! grep -q " $1\$" "$signers" || fail "A key named $1 is already trusted."
  if [ ! -f "$key.pub" ]; then
    printf 'Connect only the YubiKey for %s. Enter its PIN, then touch it. Leave the passphrase empty.\n' "$1"
    "$ssh_keygen" -t ed25519-sk -O resident -O verify-required -O application=ssh:sliplane-release -C "$1" -f "$key" \
      || fail 'Key creation failed. Check that the YubiKey is connected and has a FIDO2 PIN (ykman fido info). On macOS, run: brew install openssh && brew unlink openssh'
  fi
  line="sliplane-release namespaces=\"git,sliplane-release\" $(cut -d' ' -f1,2 "$key.pub") $1"
  printf '%s\n' "$line" >> "$signers"
  awk -v line="$line" '/<<.SIGNERS.$/ { inside=1 } inside && $0 == "SIGNERS" { print line; inside=0 } { print }' \
    "$installer" > "$work/install.sh" && cat "$work/install.sh" > "$installer"
  printf 'Trusted %s. Test it with `make signing-check KEY=%s`, then merge the change to main.\n' "$1" "$1"
}

check() {
  use_key "$1"
  [ -f "$key" ] || fail "No key file at $key. Restore it from the YubiKey with: $ssh_keygen -K"
  awk '/<<.SIGNERS.$/ { inside=1; next } $0 == "SIGNERS" { exit } inside' "$installer" > "$work/allowed_signers"
  printf 'sliplane signing check\n' > "$work/test.txt"
  "$ssh_keygen" -Y sign -q -f "$key" -n sliplane-release "$work/test.txt"
  # Installs verify with the system ssh-keygen.
  ssh-keygen -Y verify -f "$work/allowed_signers" -I sliplane-release -n sliplane-release -s "$work/test.txt.sig" < "$work/test.txt" \
    || fail "install.sh does not accept signatures from $1."
}

tag() {
  use_key "$2"
  [ -z "$(git -C "$root" status --porcelain --untracked-files=no)" ] || fail 'Commit or stash your changes first.'
  git -C "$root" fetch --quiet origin main
  [ "$(git -C "$root" rev-parse HEAD)" = "$(git -C "$root" rev-parse origin/main)" ] || fail 'Check out the latest main first.'
  git -C "$root" -c gpg.format=ssh -c gpg.ssh.program="$ssh_keygen" -c user.signingkey="$key" \
    tag -s "$1" -m "Release $1"
  git -C "$root" push origin "$1"
}

sign() {
  version=$1
  use_key "$2"
  [ -f "$key" ] || fail "No key file at $key."
  goreleaser --version 2>/dev/null | grep -q "GitVersion: *$goreleaser_version\$" \
    || fail "Use GoReleaser $goreleaser_version, the version CI uses, so the rebuild can match."

  printf '==> Verifying the signature on %s\n' "$version"
  git -C "$root" fetch --quiet --force origin main "refs/tags/$version:refs/tags/$version"
  git -C "$root" show origin/main:.github/allowed_signers > "$work/allowed_signers"
  git -C "$root" -c gpg.ssh.program="$ssh_keygen" -c gpg.ssh.allowedSignersFile="$work/allowed_signers" verify-tag "$version"

  printf '==> Downloading the draft release\n'
  [ "$(gh release view "$version" --repo "$repository" --json isDraft --jq .isDraft)" = true ] \
    || fail "$version is not a draft release."
  gh release download "$version" --repo "$repository" --dir "$work/draft"
  [ ! -e "$work/draft/checksums.txt.sig" ] || fail 'The draft is already signed.'

  printf '==> Rebuilding %s locally\n' "$version"
  # A full clone, unlike a worktree, lets Go embed the same VCS metadata as CI.
  git clone --quiet --no-local --branch "$version" "$root" "$work/source" 2>/dev/null
  go_version=$(awk '$1 == "go" { print $2 }' "$work/source/go.mod")
  (cd "$work/source" && env -u GITHUB_TOKEN -u GH_TOKEN GOTOOLCHAIN="go$go_version" goreleaser release --clean --skip=publish >/dev/null) \
    || fail 'The local rebuild failed.'

  printf '==> Comparing the draft with the local build\n'
  diff "$work/draft/checksums.txt" "$work/source/dist/checksums.txt" >&2 \
    || fail 'The draft does not match the local rebuild. Do not publish it.'
  (cd "$work/draft" && shasum -a 256 -c checksums.txt >/dev/null) || fail 'A draft asset does not match its checksum.'
  for asset in "$work/draft"/*; do
    name=${asset##*/}
    [ "$name" = checksums.txt ] || grep -q "  $name\$" "$work/draft/checksums.txt" || fail "Unexpected draft asset: $name"
  done

  printf '==> Signing checksums.txt\n'
  "$ssh_keygen" -Y sign -q -f "$key" -n sliplane-release "$work/draft/checksums.txt"
  "$ssh_keygen" -Y verify -f "$work/allowed_signers" -I sliplane-release -n sliplane-release \
    -s "$work/draft/checksums.txt.sig" < "$work/draft/checksums.txt" >/dev/null || fail "$2 is not trusted on main."
  gh release upload "$version" --repo "$repository" "$work/draft/checksums.txt.sig"

  printf 'Publish %s now? (y/N) ' "$version"
  read -r answer
  case "$answer" in
    y|Y|yes) gh release edit "$version" --repo "$repository" --draft=false >/dev/null && printf 'Published %s.\n' "$version" ;;
    *) printf 'Left %s as a signed draft.\n' "$version" ;;
  esac
}

case "${1:-}" in
  enroll|check) [ "$#" -eq 2 ] || usage; "$1" "$2" ;;
  tag|sign) [ "$#" -eq 3 ] && [ -n "$2" ] || usage; "$1" "$2" "$3" ;;
  *) usage ;;
esac
