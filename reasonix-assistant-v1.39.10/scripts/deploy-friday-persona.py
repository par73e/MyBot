#!/usr/bin/env python3
"""Install the tested Friday persona update while retaining paired state."""
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import pwd
import shutil
import subprocess
import time
import tomllib


def command(*args):
    return subprocess.run(args, capture_output=True, text=True, check=True).stdout


def atomic(path, body, mode, uid=0, gid=0):
    temporary = path.with_name(path.name + '.friday-next')
    temporary.write_bytes(body)
    temporary.chmod(mode)
    os.chown(temporary, uid, gid)
    os.replace(temporary, path)


def pointer(path, target):
    temporary = path.with_name(path.name + '.friday-next')
    temporary.symlink_to(target)
    os.replace(temporary, path)


def main():
    if os.geteuid() != 0:
        raise RuntimeError('Run as root')
    root = Path('/home/ubuntu/Project/MyBot/reasonix')
    home = Path('/var/lib/mybot/reasonix')
    account = pwd.getpwnam('mybot')
    config = home / 'config.toml'
    before = config.read_bytes()
    old = tomllib.loads(before.decode())
    if any(k in old.get('agent', {}) for k in ('system_prompt', 'system_prompt_file')):
        raise RuntimeError('Existing custom persona requires review')
    fallback = '你叫 Friday，是 Sir 的人工智能私人助手。自然简短地回应日常交流；根据当前有效记忆和待办协助用户，仅在实际提出编程需求时进入开发工作。'
    changed = before.decode().replace('[agent]\n', '[agent]\nsystem_prompt_file = "prompts/friday.md"\nsystem_prompt = ' + json.dumps(fallback, ensure_ascii=False) + '\n', 1).encode()
    parsed = tomllib.loads(changed.decode())
    assert parsed['agent']['system_prompt_file'] == 'prompts/friday.md'
    comparable = json.loads(json.dumps(parsed))
    for key in ('system_prompt_file', 'system_prompt'):
        del comparable['agent'][key]
    assert comparable == old
    binary = root / 'bin/reasonix-linux-amd64-friday'
    version = 'v1.39.10-assistant.2'
    assert version in command(str(binary), '--version')
    current = Path('/opt/mybot/current')
    old_target = os.readlink(current)
    release = Path('/opt/mybot/releases') / version
    if release.exists():
        raise RuntimeError('Release already exists')
    prompt = home / 'prompts/friday.md'
    if prompt.exists():
        raise RuntimeError('Prompt file already exists')
    credential_paths = [home / '.env', home / 'weixin/accounts/default.json']
    credentials = {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in credential_paths}
    memory_before = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in (home / 'assistant/memory').glob('*.md')}
    stamp = dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    backup = Path('/var/lib/mybot/backups') / ('friday-persona-' + stamp)
    backup.mkdir(mode=0o700)
    atomic(backup / 'config.toml', before, 0o600)
    atomic(backup / 'previous-release.txt', old_target.encode(), 0o600)
    release.mkdir(mode=0o755)
    shutil.copyfile(binary, release / 'reasonix')
    (release / 'reasonix').chmod(0o755)
    prompt.parent.mkdir(mode=0o700, exist_ok=True)
    os.chown(prompt.parent, account.pw_uid, account.pw_gid)
    since = str(int(time.time()))
    try:
        atomic(prompt, (root / 'deploy/tencent/friday.md').read_bytes(), 0o600, account.pw_uid, account.pw_gid)
        atomic(config, changed, 0o600, account.pw_uid, account.pw_gid)
        pointer(current, str(release))
        command('systemctl', 'restart', 'mybot-heartbeat.service')
        ready = False
        for _ in range(40):
            state = command('systemctl', 'is-active', 'mybot-heartbeat.service').strip()
            journal = command('journalctl', '-u', 'mybot-heartbeat.service', '--since', '@' + since, '--no-pager', '-o', 'cat')
            if state == 'active' and 'weixin getupdates ready' in journal:
                ready = True
                break
            time.sleep(1)
        if not ready:
            raise RuntimeError('Weixin polling did not become ready')
        assert credentials == {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in credential_paths}
        assert memory_before == {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in (home / 'assistant/memory').glob('*.md')}
        result = {'passed': True, 'version': version, 'prompt_path': str(prompt), 'weixin_poll_ready': True,
                  'credentials_unchanged': True, 'memory_files_unchanged': True,
                  'only_persona_config_changed': True, 'backup': str(backup),
                  'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest()}
        atomic(backup / 'deployment.json', json.dumps(result, indent=2).encode(), 0o600)
        print(json.dumps(result, ensure_ascii=False))
    except Exception:
        atomic(config, before, 0o600, account.pw_uid, account.pw_gid)
        pointer(current, old_target)
        command('systemctl', 'restart', 'mybot-heartbeat.service')
        raise


if __name__ == '__main__':
    main()
