import io
import os
import sqlite3
import tarfile
import pytest
from cryptography.hazmat.primitives.asymmetric import rsa
from deploy.secure_backup import archive_bytes, encrypt_archive, decrypt_archive, restore_to_new_directory, require_stopped


@pytest.fixture(scope='module')
def keys():
    return rsa.generate_private_key(public_exponent=65537, key_size=3072)


def test_encrypted_backup_round_trip_and_sqlite_restoration(tmp_path, keys):
    src = tmp_path / 'data'; src.mkdir()
    (src / 'private.txt').write_text('fixture-private-value')
    os.link(src / 'private.txt', src / 'hardlink.txt')
    with sqlite3.connect(src / 'state.sqlite3') as db:
        db.execute('CREATE TABLE content(value TEXT)')
        db.execute('INSERT INTO content VALUES(?)', ('fixture-database-value',))
    ciphertext = encrypt_archive(archive_bytes({'friday/reasonix':src}), keys.public_key())
    assert b'fixture-private-value' not in ciphertext and b'fixture-database-value' not in ciphertext
    target = tmp_path / 'inspect'
    restore_to_new_directory(decrypt_archive(ciphertext, keys), target)
    assert (target / 'friday/reasonix/private.txt').read_text() == 'fixture-private-value'
    assert (target / 'friday/reasonix/hardlink.txt').read_text() == 'fixture-private-value'
    with sqlite3.connect(target / 'friday/reasonix/state.sqlite3') as db:
        assert db.execute('SELECT value FROM content').fetchone()[0] == 'fixture-database-value'
    with pytest.raises(ValueError): restore_to_new_directory(decrypt_archive(ciphertext, keys), target)


def test_rejects_tampering_wrong_keys_and_unsafe_members(tmp_path, keys):
    src = tmp_path / 'data'; src.mkdir(); (src / 'value').write_text('private')
    ciphertext = encrypt_archive(archive_bytes({'friday/reasonix':src}), keys.public_key())
    with pytest.raises(ValueError): decrypt_archive(ciphertext[:-3] + b'xxx', keys)
    other = rsa.generate_private_key(public_exponent=65537, key_size=3072)
    with pytest.raises(ValueError): decrypt_archive(ciphertext, other)
    stream = io.BytesIO()
    with tarfile.open(fileobj=stream, mode='w:gz') as tar:
        item = tarfile.TarInfo('../escape'); item.size = 1; tar.addfile(item, io.BytesIO(b'x'))
    with pytest.raises(ValueError): restore_to_new_directory(stream.getvalue(), tmp_path / 'unsafe')
    assert not (tmp_path / 'escape').exists() and not (tmp_path / 'unsafe').exists()


def test_rejects_symlinks_and_small_keys(tmp_path):
    src = tmp_path / 'data'; src.mkdir(); (src / 'link').symlink_to('/etc')
    with pytest.raises(ValueError): archive_bytes({'friday/reasonix':src})
    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    with pytest.raises(ValueError): encrypt_archive(b'private', key.public_key())


def test_backup_fails_closed_if_service_state_is_unknown(monkeypatch):
    from types import SimpleNamespace
    monkeypatch.setattr('deploy.secure_backup.subprocess.run', lambda *a, **k: SimpleNamespace(stdout='LoadState=not-found\nActiveState=inactive\n'))
    with pytest.raises(ValueError, match='not confirmed inactive'): require_stopped()


def test_backup_preserves_sqlite_wal_for_recovery(tmp_path, keys):
    src=tmp_path/'data';src.mkdir()
    db=sqlite3.connect(src/'state.sqlite3')
    try:
        db.execute('PRAGMA journal_mode=WAL')
        db.execute('PRAGMA wal_autocheckpoint=0')
        db.execute('CREATE TABLE data(value TEXT)')
        db.execute('INSERT INTO data VALUES(?)',('wal-fixture',));db.commit()
        assert (src/'state.sqlite3-wal').exists()
        encrypted=encrypt_archive(archive_bytes({'asrobot/data':src}),keys.public_key())
        target=tmp_path/'restore';restore_to_new_directory(decrypt_archive(encrypted,keys),target)
        with sqlite3.connect(target/'asrobot/data/state.sqlite3') as restored:
            assert restored.execute('SELECT value FROM data').fetchone()[0]=='wal-fixture'
    finally:
        db.close()
