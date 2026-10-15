#!/usr/bin/env python3
"""Exercise read-only installer gates without installing on the build host.

Covers the payload verification chain added in this change: manifest/files.sha256
extraction, per-file digest checks, archive member safety and the optional
--pubkey signature gate.
"""
import hashlib
import io
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

root = Path(__file__).resolve().parent.parent
commands = ['id', 'awk', 'grep', 'tail', 'tar', 'gzip', 'cp', 'chmod', 'mkdir', 'mv', 'rm', 'mktemp', 'df', 'sha256sum']
with tempfile.TemporaryDirectory(prefix='andey-installer-test-') as scratch:
    folder = Path(scratch)
    tools = folder / 'bin'
    tools.mkdir()
    for name in commands:
        source = shutil.which(name)
        if not source:
            raise RuntimeError('test host lacks ' + name)
        (tools / name).symlink_to(source)
    uname = tools / 'uname'
    uname.write_text('#!/bin/sh\ncase "$1" in -s) echo Linux ;; -m) echo "${TEST_MACHINE:-x86_64}" ;; esac\n')
    uname.chmod(0o755)

    # A harmless fixture executable, not a release binary or production signature.
    binary = b'#!/bin/sh\necho 1.5.0\n'
    binary_digest = hashlib.sha256(binary).hexdigest()

    def build_payload(files, manifest_extra=None, drop=(), extra_members=(), prefix='', links=()):
        """files: mapping name -> bytes, mirrored by files.sha256 and manifest.json."""
        listing = ''.join('%s  %s\n' % (hashlib.sha256(data).hexdigest(), name)
                          for name, data in sorted(files.items()))
        listing_bytes = listing.encode()
        manifest = ('{"version":"1.5.0","goos":"linux","goarch":"x86_64","size":%d,"sha256":"%s","filesSha256":"%s"}'
                    % (len(files['andey-proxy']), binary_digest, hashlib.sha256(listing_bytes).hexdigest()))
        if manifest_extra is not None:
            manifest = manifest_extra
        members = dict(files)
        members['files.sha256'] = listing_bytes
        members['manifest.json'] = manifest.encode()
        members['manifest.sig'] = b'Zml4dHVyZQ=='  # base64 fixture, never a production signature
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode='w:gz') as payload:
            for name, data in members.items():
                if name in drop:
                    continue
                info = tarfile.TarInfo(prefix + name)
                info.size = len(data)
                info.mode = 0o755 if name == 'andey-proxy' else 0o644
                payload.addfile(info, io.BytesIO(data))
            for name, data in extra_members:
                info = tarfile.TarInfo(name)
                info.size = len(data)
                payload.addfile(info, io.BytesIO(data))
            for name, target, kind in links:
                info = tarfile.TarInfo(name)
                info.type = kind
                info.linkname = target
                payload.addfile(info)
        return archive.getvalue()

    header = (root / 'scripts/run-install.sh').read_text()
    for key, value in {'VERSION': '1.5.0', 'ARCH': 'x86_64', 'BINARY_SIZE': str(len(binary)), 'BINARY_SHA256': binary_digest}.items():
        header = header.replace('__' + key + '__', value)

    def package(name, payload, header_text=None):
        installer = folder / name
        installer.write_bytes((header_text or header).encode() + payload)
        return installer

    good = package('fixture.run', build_payload({'andey-proxy': binary}))
    env = dict(os.environ, PATH=str(tools), TMPDIR=str(folder))

    def check(expected, fragment, extra=None, path=None, args=('--check',)):
        result = subprocess.run(['/bin/sh', str(path or good), *args],
                                env={**env, **(extra or {})}, text=True, errors='replace', capture_output=True)
        output = result.stdout + result.stderr
        assert (result.returncode == 0) == expected, output
        assert fragment in output, output

    check(True, '未安装、未修改配置、未启停服务')
    check(True, '清单与摘要一致')
    check(True, '清单与摘要一致', path=package('dot-prefix.run', build_payload({'andey-proxy': binary}, prefix='./')))
    check(False, '架构', {'TEST_MACHINE': 'aarch64'})

    for kind in (tarfile.SYMTYPE, tarfile.LNKTYPE):
        linked = build_payload({'andey-proxy': binary}, links=(('alias', 'andey-proxy', kind),))
        check(False, '不允许链接或特殊文件', path=package('linked.run', linked))
    unlisted = build_payload({'andey-proxy': binary}, extra_members=(('luci/extra.js', b'not signed'),))
    check(False, '未列入已验证清单', path=package('unlisted.run', unlisted))

    # Tampered payload: content differs from the signed listing.
    tampered = build_payload({'andey-proxy': binary}, drop=())
    with io.BytesIO(tampered) as raw, tarfile.open(fileobj=raw, mode='r:gz') as archive:
        members = {m.name: archive.extractfile(m).read() for m in archive.getmembers()}
    members['andey-proxy'] = binary + b'# injected\n'
    listing = members.pop('files.sha256')
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode='w:gz') as archive:
        for name, data in members.items():
            info = tarfile.TarInfo(name)
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))
        info = tarfile.TarInfo('files.sha256')
        info.size = len(listing)
        archive.addfile(info, io.BytesIO(listing))
    check(False, '文件摘要不匹配', path=package('tampered.run', buffer.getvalue()))

    # Archive member escaping the extraction directory.
    traversal = package('traversal.run', build_payload({'andey-proxy': binary},
                                                       extra_members=[('../evil.sh', b'#!/bin/sh\n')]))
    check(False, '不安全的成员路径', path=traversal)

    # files.sha256 not covered by the signed manifest.
    mismatch = build_payload({'andey-proxy': binary},
                             manifest_extra='{"version":"1.5.0","goos":"linux","goarch":"x86_64","size":%d,"sha256":"%s","filesSha256":"%s"}'
                             % (len(binary), binary_digest, '0' * 64))
    check(False, '文件清单与签名清单不一致', path=package('mismatch.run', mismatch))

    # Missing manifest member entirely.
    check(False, '缺少 files.sha256', path=package('nolist.run', build_payload({'andey-proxy': binary}, drop=('files.sha256',))))

    # --pubkey gates: missing file, then no openssl in PATH.
    pubkey = folder / 'release.pub'
    pubkey.write_text('not-a-real-key')
    check(False, '公钥文件不存在', path=good, args=('--check', '--pubkey', str(folder / 'absent.pub')))
    check(False, '缺少 openssl', path=good, args=('--check', '--pubkey', str(pubkey)))

    # Environment gates.
    gzip = tools / 'gzip'
    gzip.unlink()
    check(False, '缺少命令 gzip')
    gzip.symlink_to(shutil.which('gzip'))
    df = tools / 'df'
    df.unlink()
    df.write_text('#!/bin/sh\necho "Filesystem 1024-blocks Used Available Capacity Mounted"\necho "fixture 100 99 1 99% /"\n')
    df.chmod(0o755)
    check(False, '可用1KiB')
    df.unlink()
    df.symlink_to(shutil.which('df'))

    corrupt = package('corrupt.run', build_payload({'andey-proxy': binary}),
                      header_text=header.replace(binary_digest, '0' * 64))
    check(False, 'SHA256不匹配', path=corrupt)

print('Installer gates passed: check-only, digest chain, payload tamper, path traversal, '
      'manifest mismatch, missing listing, pubkey gates, CPU mismatch, missing tool, '
      'low disk space, corrupt payload.')
