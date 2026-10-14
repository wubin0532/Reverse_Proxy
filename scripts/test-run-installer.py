#!/usr/bin/env python3
"""Exercise read-only installer gates without installing on the build host."""
import hashlib
import io
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

root = Path(__file__).resolve().parent.parent
commands = ['id', 'awk', 'tail', 'tar', 'gzip', 'cp', 'chmod', 'mkdir', 'mv', 'rm', 'mktemp', 'df', 'sha256sum']
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
    binary = b'#!/bin/sh\necho 1.0.0\n'
    archive = io.BytesIO()
    with tarfile.open(fileobj=archive, mode='w:gz') as payload:
        for name, data in [('andey-proxy', binary), ('manifest.json', b'{}'), ('manifest.sig', b'fixture')]:
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.mode = 0o755 if name == 'andey-proxy' else 0o644
            payload.addfile(info, io.BytesIO(data))
    header = (root / 'scripts/run-install.sh').read_text()
    for key, value in {'VERSION': '1.0.0', 'ARCH': 'x86_64', 'BINARY_SIZE': str(len(binary)), 'BINARY_SHA256': hashlib.sha256(binary).hexdigest()}.items():
        header = header.replace('__' + key + '__', value)
    installer = folder / 'fixture.run'
    installer.write_bytes(header.encode() + archive.getvalue())
    env = dict(os.environ, PATH=str(tools), TMPDIR=str(folder))
    def check(expected, fragment, extra=None, path=installer):
        result = subprocess.run(['/bin/sh', str(path), '--check'], env={**env, **(extra or {})}, text=True, errors="replace", capture_output=True)
        output = result.stdout + result.stderr
        assert (result.returncode == 0) == expected, output
        assert fragment in output, output
    check(True, '未安装、未修改配置、未启停服务')
    check(False, '架构', {'TEST_MACHINE': 'aarch64'})
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
    corrupt = folder / 'corrupt.run'
    corrupt.write_bytes(header.replace(hashlib.sha256(binary).hexdigest(), '0' * 64).encode() + archive.getvalue())
    check(False, 'SHA256不匹配', path=corrupt)
print('Installer gates passed: check-only, CPU mismatch, missing tool, low disk space, corrupt payload.')
