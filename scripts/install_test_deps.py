#!/usr/bin/env python3
# Copyright 2026 Marc-Antoine Ruel. All rights reserved.
# Use of this source code is governed under the Apache License, Version 2.0
# that can be found in the LICENSE file.

"""Install pinned compiler and runtime dependencies for generated SDK tests."""

import gzip
import json
import os
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.request
import zipfile
from dataclasses import dataclass
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
DEPS = REPO_ROOT / ".test-deps"
TYPESCRIPT_VERSION = "6.0.3"
SWIFT_VERSION = "6.4.1"
SWIFT_KEYS_URL = "https://www.swift.org/keys/all-keys.asc"
CHUNK = 1 << 20


@dataclass(frozen=True)
class KotlinDependencies:
    kotlin: str
    okhttp: str
    okio: str
    coroutines: str
    serialization: str


def download(url: str, dest: Path, work: Path) -> None:
    """Stream url to dest unless dest exists, publishing it with one rename.

    It decodes a gzip Content-Encoding itself: urllib does not, and the
    swift.org CDN sends gzip even when the request asks for identity.
    """
    if dest.exists():
        return
    print(f"Downloading {url}", flush=True)
    tmp = work / "download"
    for attempt in range(3):
        try:
            with urllib.request.urlopen(url, timeout=60) as resp, tmp.open("wb") as out:
                body = gzip.GzipFile(fileobj=resp) if resp.headers.get("Content-Encoding") == "gzip" else resp
                shutil.copyfileobj(body, out, CHUNK)
            break
        except (urllib.error.URLError, OSError, EOFError) as e:
            if attempt == 2:
                raise RuntimeError(f"download {url}: {e}") from e
            time.sleep(2**attempt)
    tmp.replace(dest)


def link(target: str, path: Path, work: Path) -> None:
    """Point the symlink at path to target, replacing any previous link atomically."""
    tmp = work / "link"
    tmp.unlink(missing_ok=True)
    tmp.symlink_to(target)
    tmp.replace(path)


def extract_zip(archive: Path, dest: Path) -> None:
    """Extract archive into dest, keeping Unix permission bits that zipfile drops."""
    with zipfile.ZipFile(archive) as z:
        for info in z.infolist():
            path = Path(z.extract(info, dest))
            if mode := (info.external_attr >> 16) & 0o777:
                path.chmod(mode)


def install_kotlin(deps: KotlinDependencies, work: Path) -> None:
    home = DEPS / f"kotlin-{deps.kotlin}"
    if not os.access(home / "bin" / "kotlinc", os.X_OK):
        archive = work / "kotlin.zip"
        download(
            f"https://github.com/JetBrains/kotlin/releases/download/v{deps.kotlin}/kotlin-compiler-{deps.kotlin}.zip",
            archive,
            work,
        )
        extract_zip(archive, work / "kotlin")
        (work / "kotlin" / "kotlinc").replace(home)
    link(home.name, DEPS / "kotlin", work)
    link("../kotlin/bin/kotlinc", DEPS / "bin" / "kotlinc", work)

    # JVM runtime dependencies; the compiler supplies the matching Kotlin stdlib.
    jars: list[str] = []
    old_jars: list[Path] = []
    for group, artifact, version in [
        ("com.squareup.okhttp3", "okhttp", deps.okhttp),
        ("com.squareup.okhttp3", "okhttp-sse", deps.okhttp),
        ("com.squareup.okio", "okio-jvm", deps.okio),
        ("org.jetbrains.kotlinx", "kotlinx-coroutines-core-jvm", deps.coroutines),
        ("org.jetbrains.kotlinx", "kotlinx-coroutines-test-jvm", deps.coroutines),
        ("org.jetbrains.kotlinx", "kotlinx-serialization-core-jvm", deps.serialization),
        ("org.jetbrains.kotlinx", "kotlinx-serialization-json-jvm", deps.serialization),
    ]:
        jar = f"{artifact}-{version}.jar"
        url = f"https://repo.maven.apache.org/maven2/{group.replace('.', '/')}/{artifact}/{version}/{jar}"
        download(url, DEPS / "jars" / jar, work)
        jars.append(f"jars/{jar}")
        # A numeric version distinguishes okhttp releases from okhttp-sse artifacts.
        versioned = re.compile(re.escape(artifact) + r"-[0-9].*\.jar")
        old_jars += [p for p in (DEPS / "jars").iterdir() if versioned.fullmatch(p.name) and p.name != jar]

    # Publish only the pinned, completely downloaded JARs; unrelated cached JARs
    # must not enter the compiler or runtime classpath.
    (work / "kotlin-jars").write_text("".join(j + "\n" for j in jars), encoding="utf-8")
    (work / "kotlin-jars").replace(DEPS / "kotlin-jars")
    for p in old_jars:
        p.unlink()


def install_typescript(npm: str) -> None:
    tsc = DEPS / "node_modules" / ".bin" / "tsc"
    if tsc.exists():
        got = subprocess.run([tsc, "--version"], check=True, capture_output=True, text=True).stdout.strip()
        if got == f"Version {TYPESCRIPT_VERSION}":
            return
    subprocess.run(
        [
            npm,
            "install",
            "--prefix",
            DEPS,
            "--no-save",
            "--package-lock=false",
            "--ignore-scripts",
            f"typescript@{TYPESCRIPT_VERSION}",
        ],
        check=True,
    )


def swift_platform() -> tuple[str, str]:
    """Return the swift.org download directory and archive suffix for this host."""
    release = platform.freedesktop_os_release()
    distro, version = release["ID"], release["VERSION_ID"]
    # swift.org names Ubuntu archives with the dotted version, Debian ones without.
    names = {"ubuntu": f"ubuntu{version}", "debian": f"debian{version}"}
    if distro not in names:
        raise RuntimeError(f"unsupported Linux distribution for the Swift toolchain: {distro} {version}")
    arches = {"x86_64": "", "aarch64": "-aarch64"}
    if (machine := platform.machine()) not in arches:
        raise RuntimeError(f"unsupported architecture for the Swift toolchain: {machine}")
    arch = arches[machine]
    return distro + version.replace(".", "") + arch, names[distro] + arch


def install_swift(gpg: str, gpgv: str, work: Path) -> None:
    home = DEPS / f"swift-{SWIFT_VERSION}"
    if not os.access(home / "usr" / "bin" / "swiftc", os.X_OK):
        directory, suffix = swift_platform()
        tag = f"swift-{SWIFT_VERSION}-RELEASE"
        url = f"https://download.swift.org/swift-{SWIFT_VERSION}-release/{directory}/{tag}/{tag}-{suffix}.tar.gz"
        archive, sig, keys = work / "swift.tar.gz", work / "swift.tar.gz.sig", work / "swift-keys.asc"
        download(url, archive, work)
        download(url + ".sig", sig, work)
        download(SWIFT_KEYS_URL, keys, work)
        # gpgv checks the signature against this keyring alone, without a
        # gpg-agent or a persistent GnuPG home; gpg only converts the keys.
        gnupg = work / "gnupg"
        gnupg.mkdir(mode=0o700)
        keyring = work / "swift-keys.gpg"
        env = {**os.environ, "GNUPGHOME": str(gnupg)}
        subprocess.run([gpg, "--batch", "--dearmor", "--output", keyring, keys], check=True, env=env)
        subprocess.run([gpgv, "--keyring", keyring, sig, archive], check=True)
        with tarfile.open(archive, "r:gz") as t:
            t.extractall(work / "swift", filter="data")
        (work / "swift" / f"{tag}-{suffix}").replace(home)
    link(home.name, DEPS / "swift", work)
    for tool in ("swift", "swiftc"):
        link(f"../swift/usr/bin/{tool}", DEPS / "bin" / tool, work)
    subprocess.run([DEPS / "bin" / "swiftc", "--version"], check=True)


def main() -> int:
    tools = {t: shutil.which(t) for t in ("gpg", "gpgv", "java", "npm")}
    if missing := [t for t, p in tools.items() if p is None]:
        print(f"Required command missing: {', '.join(missing)}", file=sys.stderr)
        return 1
    (DEPS / "bin").mkdir(parents=True, exist_ok=True)
    (DEPS / "jars").mkdir(exist_ok=True)
    with (REPO_ROOT / "kotlin-dependencies.json").open(encoding="utf-8") as f:
        # The generated package manifests and this test classpath share one version owner.
        kotlin = KotlinDependencies(**json.load(f))
    # Stage beside the installed files so publication uses same-filesystem renames.
    with tempfile.TemporaryDirectory(prefix=".install-", dir=DEPS) as tmp:
        work = Path(tmp)
        install_kotlin(kotlin, work)
        install_typescript(tools["npm"])
        if sys.platform == "linux":
            install_swift(tools["gpg"], tools["gpgv"], work)
    return 0


if __name__ == "__main__":
    sys.exit(main())
