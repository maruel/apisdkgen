#!/bin/bash
# Install pinned compiler and runtime dependencies for generated SDK tests.
set -euo pipefail

script_dir="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly script_dir
repo_root="$(CDPATH='' cd -- "$script_dir/.." && pwd)"
readonly repo_root
deps="$repo_root/.test-deps"
readonly deps

for tool in curl gpg java npm tar unzip; do
  command -v "$tool" >/dev/null || {
    printf 'Required command missing: %s\n' "$tool" >&2
    exit 1
  }
done
work_dir="$(mktemp -d)"
readonly work_dir
trap 'rm -rf -- "$work_dir"' EXIT
mkdir -p -- "$deps/bin" "$deps/jars"

download() {
  local url="$1" dest="$2"
  if [[ ! -f "$dest" ]]; then
    printf 'Downloading %s\n' "$url"
    curl --fail --location --silent --show-error --retry 3 "$url" -o "$work_dir/download"
    mv -- "$work_dir/download" "$dest"
  fi
}

kotlin_version=2.4.21
if [[ ! -x "$deps/kotlin-$kotlin_version/bin/kotlinc" ]]; then
  download "https://github.com/JetBrains/kotlin/releases/download/v$kotlin_version/kotlin-compiler-$kotlin_version.zip" "$work_dir/kotlin.zip"
  unzip -q "$work_dir/kotlin.zip" -d "$work_dir/kotlin"
  mv -- "$work_dir/kotlin/kotlinc" "$deps/kotlin-$kotlin_version"
fi
ln -sfn -- "kotlin-$kotlin_version" "$deps/kotlin"
ln -sfn -- ../kotlin/bin/kotlinc "$deps/bin/kotlinc"

# JVM runtime dependencies; the compiler supplies the matching Kotlin stdlib.
while read -r group artifact version; do
  jar="$artifact-$version.jar"
  download "https://repo.maven.apache.org/maven2/${group//.//}/$artifact/$version/$jar" "$deps/jars/$jar"
  for old in "$deps/jars/$artifact-"*.jar; do
    if [[ "$old" != "$deps/jars/$jar" ]]; then rm -- "$old"; fi
  done
done <<'JARS'
com.squareup.okhttp3 okhttp-jvm 5.5.1
com.squareup.okhttp3 okhttp-sse 5.5.1
com.squareup.okio okio-jvm 3.18.1
org.jetbrains.kotlinx kotlinx-coroutines-core-jvm 1.11.0
org.jetbrains.kotlinx kotlinx-serialization-core-jvm 1.12.0
org.jetbrains.kotlinx kotlinx-serialization-json-jvm 1.12.0
JARS

if [[ ! -x "$deps/node_modules/.bin/tsc" ]] || [[ "$("$deps/node_modules/.bin/tsc" --version)" != 'Version 6.0.3' ]]; then
  npm install --prefix "$deps" --no-save --package-lock=false --ignore-scripts typescript@6.0.3
fi

# Swiftly selects the matching Linux distribution and checks system libraries.
# Keep its config and toolchains inside the checkout without editing shell profiles.
if [[ "$(uname -s)" == Linux ]]; then
  export SWIFTLY_HOME_DIR="$deps/swiftly"
  export SWIFTLY_BIN_DIR="$deps/bin"
  export SWIFTLY_TOOLCHAINS_DIR="$deps/swiftly/toolchains"
  export GNUPGHOME="$deps/gnupg"
  mkdir -p -- "$GNUPGHOME"
  chmod 700 -- "$GNUPGHOME"
  if [[ ! -x "$deps/bin/swiftly" ]]; then
    download "https://download.swift.org/swiftly/linux/swiftly-1.2.0-$(uname -m).tar.gz" "$work_dir/swiftly.tar.gz"
    tar -xzf "$work_dir/swiftly.tar.gz" -C "$work_dir"
    "$work_dir/swiftly" init --no-modify-profile --skip-install --assume-yes --quiet-shell-followup
  fi
  "$deps/bin/swiftly" install 6.4.1 --assume-yes
  "$deps/bin/swiftly" use 6.4.1 --global-default --assume-yes
  "$deps/bin/swiftly" link --assume-yes
  "$deps/bin/swiftc" --version
fi
