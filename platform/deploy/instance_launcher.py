"""Operator-only preparation of a single assistant container.

This creates a stopped container. A host service must reapply its network guard
before each start; the container must not use Docker's automatic restart policy.
"""
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import subprocess

spec = importlib.util.spec_from_file_location('instance_network', Path(__file__).with_name('instance_network.py'))
network_guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(network_guard)
IMAGE = 'asrobot-assistant:instance-v1'
CONTRACT = 'asrobot-instance-v1'


def _run(args, check=True):
    return subprocess.run(args, check=check, capture_output=True, text=True, timeout=60)


def container_name(instance_id):
    network_guard.names(instance_id)
    return 'asrobot-agent-' + instance_id


def create(instance_id, root, expected_image_id, run=_run, prepare_network=None):
    """Prepare one stopped, isolated container for an existing scaffold."""
    name = container_name(instance_id)
    root = Path(root)
    if not root.is_absolute() or root.is_symlink() or any(ch in str(root) for ch in ',\r\n'):
        raise ValueError('invalid instance root')
    directory = root / instance_id
    info = directory.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_mode & 0o077 or info.st_uid != 10001 or info.st_gid != 10001:
        raise ValueError('instance directory is not privately owned by runtime user')
    if not re.fullmatch(r'sha256:[0-9a-f]{64}', expected_image_id or ''):
        raise ValueError('a verified immutable image ID is required')
    for part in ('home', 'workspace', 'state', 'home/prompts'):
        child = directory / part
        info = child.lstat()
        if not stat.S_ISDIR(info.st_mode) or child.is_symlink() or info.st_uid != 10001 or info.st_mode & 0o077:
            raise ValueError('unsafe instance directory')
    for part in ('instance.json', 'native.token', 'home/config.toml', 'home/prompts/assistant.md'):
        child = directory / part
        info = child.lstat()
        if not stat.S_ISREG(info.st_mode) or child.is_symlink() or info.st_uid != 10001 or info.st_mode & 0o077:
            raise ValueError('unsafe instance file')
    manifest = json.loads((directory/'instance.json').read_text())
    if manifest != {'version':1, 'instance_id':instance_id, 'status':'prepared', 'running':False}:
        raise ValueError('instance scaffold does not match')
    if len((directory/'native.token').read_text().strip()) < 32:
        raise ValueError('instance token is invalid')
    image = json.loads(run(['docker','image','inspect',IMAGE]).stdout)
    if len(image) != 1 or image[0].get('Id') != expected_image_id or (image[0].get('Config',{}).get('Labels') or {}).get('asrobot.contract') != CONTRACT:
        raise ValueError('assistant runtime image is not verified')
    user = image[0].get('Config',{}).get('User','')
    if user not in ('10001','10001:10001'):
        raise ValueError('assistant runtime image must use the unprivileged runtime user')
    # The assistant relies on nested bubblewrap for workspace-write shell
    # isolation. Never deploy an image where this boundary cannot be created.
    try:
        run(['docker','run','--rm','--network','none','--read-only','--user','10001:10001',
             '--cap-drop','ALL','--security-opt','no-new-privileges','--pids-limit','32',
             '--memory','128m',IMAGE,'/usr/bin/bwrap','--unshare-user','--ro-bind','/','/',
             '--proc','/proc','--dev','/dev','/bin/true'])
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as exc:
        raise ValueError('assistant workspace sandbox is unavailable') from exc
    existing = run(['docker','ps','--all','--filter','name=^/'+name+'$','--format','{{.Names}}']).stdout.splitlines()
    if existing:
        raise ValueError('instance container already exists; reconcile before retrying')
    if prepare_network is None:
        prepare_network = lambda ident: network_guard.prepare(ident, run=run)
    network = prepare_network(instance_id)['network']
    runtime_root = '/instances/' + instance_id
    env = {
        'HOME':runtime_root, 'REASONIX_HOME':runtime_root+'/home', 'REASONIX_ASSISTANT':'1',
        'ASROBOT_INSTANCE_MODE':'isolated', 'ASROBOT_RUNTIME_ROOT':runtime_root,
        'ASROBOT_RUNTIME_HOME':runtime_root+'/home', 'ASROBOT_RUNTIME_WORKSPACE':runtime_root+'/workspace',
        'ASROBOT_RUNTIME_STATE':runtime_root+'/state', 'ASROBOT_RUNTIME_TOKEN':runtime_root+'/native.token',
        'ASROBOT_RUNTIME_SOCKET':runtime_root+'/native.sock', 'ASROBOT_RUNTIME_EXECUTABLE':'/opt/asrobot-agent/reasonix',
        'ASROBOT_BRIDGE_EXECUTABLE':'/opt/asrobot-agent/bridge',
        'ASROBOT_RUNTIME_UNIT':'asrobot-instance-'+instance_id+'.service', 'TZ':'Asia/Shanghai',
    }
    args=['docker','create','--name',name,'--label','asrobot.instance='+instance_id,
          '--network',network,'--restart','no','--read-only','--user','10001:10001',
          '--cap-drop','ALL','--security-opt','no-new-privileges','--pids-limit','128',
          '--memory','1g','--cpus','1','--tmpfs','/tmp:rw,noexec,nosuid,size=64m',
          '--mount','type=bind,src='+str(directory)+',dst='+runtime_root]
    for key,value in env.items(): args.extend(('--env',key+'='+value))
    args.extend((IMAGE,'python3','/opt/asrobot-agent/instance_supervisor.py'))
    run(args)
    return {'container':name,'network':network,'state':'created'}


def main():
    import argparse
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='action', required=True)
    guard = sub.add_parser('network')
    guard.add_argument('instance_id')
    create_parser = sub.add_parser('create')
    create_parser.add_argument('instance_id')
    create_parser.add_argument('--root', required=True)
    create_parser.add_argument('--image-id', required=True)
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise SystemExit('operator root required')
    if args.action == 'network':
        result = network_guard.prepare(args.instance_id)
    else:
        result = create(args.instance_id, args.root, args.image_id)
    print(json.dumps(result))


if __name__ == '__main__':
    main()
