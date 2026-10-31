import importlib.util
import os
from pathlib import Path
import sqlite3
from types import SimpleNamespace
import pytest

spec = importlib.util.spec_from_file_location('cutover_recovery',Path(__file__).parents[1]/'deploy/instance_cutover_recovery.py')
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


@pytest.mark.skipif(os.geteuid()!=0, reason='root-owned isolated fixture required')
def test_recovery_refuses_user_writes_and_restores_only_fenced_checkpoint(tmp_path, monkeypatch):
    data=tmp_path/'data';data.mkdir(mode=0o700)
    backups=tmp_path/'backups';backups.mkdir(mode=0o700)
    target=data/'platform.sqlite3'
    with sqlite3.connect(target) as db:
        db.execute('CREATE TABLE items(value TEXT)')
        db.execute("INSERT INTO items VALUES('before')")
    target.chmod(0o600);os.chown(target,10001,10001)
    monkeypatch.setattr(m.cutover,'DATA_ROOT',data)
    monkeypatch.setattr(m.cutover,'BACKUP_ROOT',backups)
    monkeypatch.setattr(m.cutover,'_assert_stopped_old_services',lambda execute:None)
    monkeypatch.setattr(m.cutover,'_assert_platform_stopped',lambda *args:None)
    monkeypatch.setattr(m.cutover.installer,'_no_processes',lambda uid:True)
    monkeypatch.setattr(m.pwd,'getpwnam',lambda name:SimpleNamespace(pw_uid=12345))
    execute=lambda args,**kw:SimpleNamespace(stdout='inactive\n' if 'is-active' in args else 'disabled\n')
    args=('a'*32,'b'*64,'sha256:'+'c'*64)
    before=m.checkpoint(*args,execute=execute)
    with sqlite3.connect(target) as db:db.execute("INSERT INTO items VALUES('migration')")
    after=m.checkpoint(*args,execute=execute)
    with sqlite3.connect(target) as db:db.execute("INSERT INTO items VALUES('new user write')")
    with pytest.raises(ValueError,match='changed after checkpoint'):
        m.restore(*args,before,after,execute=execute)
    with sqlite3.connect(target) as db:
        assert db.execute('SELECT count(*) FROM items').fetchone()[0]==3
        db.execute("DELETE FROM items WHERE value='new user write'")
    assert m.restore(*args,before,after,execute=execute)['state']=='restored'
    with sqlite3.connect(target) as db:assert db.execute('SELECT value FROM items').fetchall()==[('before',)]
    def active(args,**kw):return SimpleNamespace(stdout='active\n')
    with pytest.raises(ValueError,match='stopped and disabled'):
        m.restore(*args,before,after,execute=active)
