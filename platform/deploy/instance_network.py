"""Fail-closed per-instance network preparation, operator-side only.

Install rules before starting a container. Do not remove them until all of its
containers are stopped. Failed preparation deliberately retains inserted rules.
"""
import json
import re
import subprocess

PRIVATE_V4 = ('10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16',
              '169.254.0.0/16', '127.0.0.0/8', '100.64.0.0/10')


def names(instance_id):
    if not isinstance(instance_id, str) or not re.fullmatch(r'[0-9a-f]{32}', instance_id):
        raise ValueError('invalid instance identifier')
    return 'asrobot-instance-' + instance_id, 'asr' + instance_id[:10]


def _run(args, check=True):
    return subprocess.run(args, check=check, capture_output=True, text=True, timeout=30)


def _rules(bridge):
    return [('INPUT', ['-i', bridge, '-j', 'DROP'])] + [
        ('DOCKER-USER', ['-i', bridge, '-d', subnet, '-j', 'DROP']) for subnet in PRIVATE_V4
    ]


def prepare(instance_id, run=_run):
    """Validate bridge ownership, install rules, then create the network."""
    network, bridge = names(instance_id)
    listed = run(['docker', 'network', 'ls', '--quiet'])
    ids = listed.stdout.split()
    all_networks = json.loads(run(['docker', 'network', 'inspect', *ids]).stdout) if ids else []
    selected = None
    for item in all_networks:
        options = item.get('Options') or {}
        if item.get('Name') == network:
            selected = item
        if options.get('com.docker.network.bridge.name') == bridge and item.get('Name') != network:
            raise ValueError('instance bridge name is already in use')
    if selected:
        options, labels = selected.get('Options') or {}, selected.get('Labels') or {}
        if (selected.get('Driver') != 'bridge' or selected.get('EnableIPv6') or
                labels.get('asrobot.instance') != instance_id or
                options.get('com.docker.network.bridge.name') != bridge or
                options.get('com.docker.network.bridge.enable_icc') != 'false'):
            raise ValueError('existing network does not match the instance')
    elif run(['ip', 'link', 'show', 'dev', bridge], check=False).returncode == 0:
        raise ValueError('instance bridge name is already in use')
    for chain, rule in _rules(bridge):
        check = run(['iptables', '-w', '10', '-C', chain, *rule], check=False)
        if check.returncode == 1:
            run(['iptables', '-w', '10', '-I', chain, '1', *rule])
        elif check.returncode != 0:
            raise RuntimeError('cannot verify instance firewall rule')
    if not selected:
        run(['docker', 'network', 'create', '--driver', 'bridge', '--ipv6=false',
             '--label', 'asrobot.instance=' + instance_id,
             '--opt', 'com.docker.network.bridge.name=' + bridge,
             '--opt', 'com.docker.network.bridge.enable_icc=false', network])
    return {'network': network, 'bridge': bridge}
