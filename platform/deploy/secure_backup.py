"""Offline encrypted backup; recovery always writes to a new inspection directory.

The RSA private key must be generated and stored separately by the owner.
Backups do not include the OS, certificates, all services, or the complete
workspace outside the explicitly listed paths.
"""
import argparse
import getpass
import io
import os
import stat
import struct
import subprocess
import tarfile
from pathlib import Path

from cryptography.fernet import Fernet, InvalidToken
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import padding, rsa

MAGIC = b'ASROBOT-BACKUP-1\n'
MAX_BYTES = 256 * 1024 * 1024
ROOTS = {
    'asrobot/data': Path('/var/lib/asrobot'),
    'asrobot/master.key': Path('/var/lib/asrobot-secrets/master.key'),
    'friday/reasonix': Path('/var/lib/mybot/reasonix'),
    'friday/mail': Path('/var/lib/mybot/mail'),
    'friday/weather-qweather': Path('/var/lib/mybot/weather/qweather'),
    'friday/workspace': Path('/var/lib/mybot/workspace'),
}


def require_stopped():
    for service in ('mybot-heartbeat.service', 'asrobot-native.service', 'asrobot-history.service'):
        result = subprocess.run(['systemctl', 'show', service, '--property=LoadState',
                                 '--property=ActiveState'], capture_output=True, text=True, check=True)
        fields = dict(line.split('=', 1) for line in result.stdout.splitlines() if '=' in line)
        if fields.get('LoadState') != 'loaded' or fields.get('ActiveState') != 'inactive':
            raise ValueError(f'{service} is not confirmed inactive')
    result = subprocess.run(['docker', 'inspect', '--format', '{{.State.Running}}', 'asrobot'],
                            capture_output=True, text=True, check=True)
    if result.stdout.strip() != 'false':
        raise ValueError('asrobot container is running; stop it before the backup')


def archive_bytes(roots):
    stream = io.BytesIO()
    total = 0
    with tarfile.open(fileobj=stream, mode='w:gz', dereference=True) as tar:
        for label, root in roots.items():
            root = Path(root)
            if not root.exists() or root.is_symlink():
                raise ValueError(f'backup source missing or symbolic: {label}')
            paths = [root]
            if root.is_dir():
                for directory, subdirs, files in os.walk(root, followlinks=False):
                    paths.extend(Path(directory) / name for name in sorted(subdirs + files))
            for path in paths:
                info = path.lstat()
                if not (stat.S_ISDIR(info.st_mode) or stat.S_ISREG(info.st_mode)):
                    raise ValueError(f'unsupported file in {label}')
                total += info.st_size if stat.S_ISREG(info.st_mode) else 0
                if total > MAX_BYTES:
                    raise ValueError('backup exceeds in-memory safety limit')
                name = label if path == root else label + '/' + path.relative_to(root).as_posix()
                tar.add(path, arcname=name, recursive=False)
    return stream.getvalue()


def encrypt_archive(archive, public_key):
    if not isinstance(public_key, rsa.RSAPublicKey) or public_key.key_size < 3072:
        raise ValueError('RSA public key must be at least 3072 bits')
    key = Fernet.generate_key()
    wrapped = public_key.encrypt(key, padding.OAEP(mgf=padding.MGF1(hashes.SHA256()),
                                                   algorithm=hashes.SHA256(), label=None))
    return MAGIC + struct.pack('>I', len(wrapped)) + wrapped + Fernet(key).encrypt(archive)


def decrypt_archive(data, private_key):
    if not isinstance(private_key, rsa.RSAPrivateKey) or private_key.key_size < 3072:
        raise ValueError('invalid RSA private key')
    if len(data) > MAX_BYTES * 3 or not data.startswith(MAGIC) or len(data) < len(MAGIC) + 4:
        raise ValueError('invalid backup format')
    pos = len(MAGIC)
    size = struct.unpack('>I', data[pos:pos + 4])[0]
    pos += 4
    if size != private_key.key_size // 8 or pos + size >= len(data):
        raise ValueError('invalid wrapped key')
    key = private_key.decrypt(data[pos:pos + size], padding.OAEP(mgf=padding.MGF1(hashes.SHA256()),
                                                                 algorithm=hashes.SHA256(), label=None))
    try:
        return Fernet(key).decrypt(data[pos + size:])
    except InvalidToken as exc:
        raise ValueError('backup authentication failed') from exc


def restore_to_new_directory(archive, destination):
    dest = Path(destination)
    if dest.exists():
        raise ValueError('destination already exists')
    with tarfile.open(fileobj=io.BytesIO(archive), mode='r:gz') as tar:
        members = tar.getmembers()
        seen = set()
        total = 0
        for item in members:
            parts = Path(item.name).parts
            if (not parts or item.name.startswith('/') or '..' in parts or
                    item.name in seen or not (item.isdir() or item.isfile())):
                raise ValueError('unsafe archive member')
            seen.add(item.name)
            total += item.size
            if total > MAX_BYTES:
                raise ValueError('archive exceeds safety limit')
        dest.mkdir(mode=0o700, parents=False, exist_ok=False)
        tar.extractall(dest, members=members, filter='data')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='action', required=True)
    backup = sub.add_parser('backup'); backup.add_argument('--public-key', required=True); backup.add_argument('--output', required=True)
    restore = sub.add_parser('restore'); restore.add_argument('--private-key', required=True); restore.add_argument('--input', required=True); restore.add_argument('--directory', required=True)
    args = parser.parse_args()
    if args.action == 'backup':
        require_stopped()
        public = serialization.load_pem_public_key(Path(args.public_key).read_bytes())
        archive = archive_bytes(ROOTS)
        require_stopped()
        encrypted = encrypt_archive(archive, public)
        fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'wb') as out:
            out.write(encrypted); out.flush(); os.fsync(out.fileno())
        print('Encrypted backup created. Store a copy and the private key separately off this server.')
    else:
        key_data = Path(args.private_key).read_bytes()
        password = getpass.getpass('Private key passphrase (empty if unencrypted): ').encode() or None
        private = serialization.load_pem_private_key(key_data, password=password)
        source = Path(args.input)
        if source.stat().st_size > MAX_BYTES * 3:
            raise ValueError('backup exceeds safety limit')
        restore_to_new_directory(decrypt_archive(source.read_bytes(), private), args.directory)
        print('Decrypted to a new inspection directory; no live data was overwritten.')


if __name__ == '__main__':
    main()
