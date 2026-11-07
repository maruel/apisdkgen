#!/bin/bash
# Install pinned compiler and runtime dependencies for generated SDK tests.
set -euo pipefail

script_dir="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly script_dir
repo_root="$(CDPATH='' cd -- "$script_dir/.." && pwd)"
readonly repo_root
deps="$repo_root/.test-deps"
readonly deps

for tool in curl gpg java npm python3 tar unzip; do
  command -v "$tool" >/dev/null || {
    printf 'Required command missing: %s\n' "$tool" >&2
    exit 1
  }
done
mkdir -p -- "$deps/bin" "$deps/jars"
# Stage beside the installed files so publication uses same-filesystem renames.
work_dir="$(mktemp -d "$deps/.install-XXXXXXXX")"
readonly work_dir
trap 'rm -rf -- "$work_dir"' EXIT

download() {
  local url="$1" dest="$2"
  if [[ ! -f "$dest" ]]; then
    printf 'Downloading %s\n' "$url"
    curl --fail --location --silent --show-error --retry 3 "$url" -o "$work_dir/download"
    mv -- "$work_dir/download" "$dest"
  fi
}

# The generated package manifests and this test classpath share one version owner.
python3 - "$repo_root/kotlin-dependencies.json" >"$work_dir/kotlin-deps" <<'PYTHON'
import json
import sys
from dataclasses import dataclass


@dataclass(frozen=True)
class KotlinDependencies:
    kotlin: str
    okhttp: str
    okio: str
    coroutines: str
    serialization: str


with open(sys.argv[1], encoding="utf-8") as source:
    deps = KotlinDependencies(**json.load(source))
print(deps.kotlin)
for group, artifact, version in [
    ("com.squareup.okhttp3", "okhttp", deps.okhttp),
    ("com.squareup.okhttp3", "okhttp-sse", deps.okhttp),
    ("com.squareup.okio", "okio-jvm", deps.okio),
    ("org.jetbrains.kotlinx", "kotlinx-coroutines-core-jvm", deps.coroutines),
    ("org.jetbrains.kotlinx", "kotlinx-coroutines-test-jvm", deps.coroutines),
    ("org.jetbrains.kotlinx", "kotlinx-serialization-core-jvm", deps.serialization),
    ("org.jetbrains.kotlinx", "kotlinx-serialization-json-jvm", deps.serialization),
]:
    print(group, artifact, version)
PYTHON
read -r kotlin_version <"$work_dir/kotlin-deps"
tail -n +2 "$work_dir/kotlin-deps" >"$work_dir/kotlin-runtime"
if [[ ! -x "$deps/kotlin-$kotlin_version/bin/kotlinc" ]]; then
  download "https://github.com/JetBrains/kotlin/releases/download/v$kotlin_version/kotlin-compiler-$kotlin_version.zip" "$work_dir/kotlin.zip"
  unzip -q "$work_dir/kotlin.zip" -d "$work_dir/kotlin"
  mv -- "$work_dir/kotlin/kotlinc" "$deps/kotlin-$kotlin_version"
fi
ln -sfn -- "kotlin-$kotlin_version" "$deps/kotlin"
ln -sfn -- ../kotlin/bin/kotlinc "$deps/bin/kotlinc"

# JVM runtime dependencies; the compiler supplies the matching Kotlin stdlib.
jars=()
old_jars=()
while read -r group artifact version; do
  jar="$artifact-$version.jar"
  download "https://repo.maven.apache.org/maven2/${group//.//}/$artifact/$version/$jar" "$deps/jars/$jar"
  jars+=("jars/$jar")
  for old in "$deps/jars/$artifact-"*.jar; do
    # A numeric version distinguishes okhttp releases from okhttp-sse artifacts.
    case "${old##*/}" in
    "$artifact"-[0-9]*.jar)
      if [[ "$old" != "$deps/jars/$jar" ]]; then old_jars+=("$old"); fi
      ;;
    esac
  done
done <"$work_dir/kotlin-runtime"

# Publish only the pinned, completely downloaded JARs; unrelated cached JARs
# must not enter the compiler or runtime classpath.
printf '%s\n' "${jars[@]}" >"$work_dir/kotlin-jars"
mv -- "$work_dir/kotlin-jars" "$deps/kotlin-jars"
if ((${#old_jars[@]} > 0)); then rm -- "${old_jars[@]}"; fi

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
