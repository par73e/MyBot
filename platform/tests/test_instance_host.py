import importlib.util
import json
import os
from pathlib import Path
import pytest
spec=importlib.util.spec_from_file_location('instance_host',Path(__file__).parents[1]/'deploy'/'instance_host.py')
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)


def test_generated_host_unit_is_scoped_and_preserves_terminal_sandbox():
    ident='a'*32
    unit=module.render_unit(ident,'8.8.8.8')
    assert 'User=asr-'+ident[:20] in unit
    assert 'ReadWritePaths=/var/lib/asrobot-instances/'+ident in unit
    assert 'ASROBOT_RUNTIME_UNIT=asrobot-instance-'+ident+'.service' in unit
    assert 'NoNewPrivileges=yes' in unit and 'CapabilityBoundingSet=' in unit
    assert 'IPAddressDeny=127.0.0.0/8' in unit and 'IPAddressDeny=8.8.8.8/32' in unit
    assert 'IPAddressAllow=127.0.0.53/32' in unit
    assert 'ProtectKernelTunables=yes' not in unit  # would prevent nested /proc sandbox mount
    assert 'docker.sock' not in unit


def test_host_unit_rejects_untrusted_identity_or_address():
    for ident in ('../escape','A'*32,'a'*31):
        with pytest.raises(ValueError):module.render_unit(ident,'8.8.8.8')
    for ip in ('127.0.0.1','192.168.1.1','not-an-ip','8.8.8.8\nUser=root'):
        with pytest.raises(ValueError):module.render_unit('a'*32,ip)
    for port in (0,65536,'123',True):
        with pytest.raises(ValueError):module.render_mail_proxy_unit('a'*32,port)


def test_optional_mail_proxy_has_separate_restricted_service():
    ident='a'*32
    unit=module.render_unit(ident,'8.8.8.8',17890)
    relay=module.render_mail_proxy_unit(ident,17890)
    assert 'Wants=asrobot-mail-proxy-'+ident+'.service' in unit
    assert 'ASROBOT_MAIL_PROXY_SOCKET=/var/lib/asrobot-instances/'+ident+'/state/mail-proxy.sock' in unit
    assert 'IPAddressDeny=127.0.0.0/8' in unit
    assert 'IPAddressDeny=0.0.0.0/0' in relay and 'IPAddressAllow=127.0.0.1/32' in relay
    assert 'mail_proxy_relay.py' in relay and 'ASROBOT_MAIL_PROXY_PORT=17890' in relay


def test_reviewed_template_digests_match_the_scaffold():
    import hashlib
    from app.instance_scaffold import EMPTY_CONFIG, EMPTY_PROMPT
    assert hashlib.sha256(EMPTY_CONFIG.encode()).hexdigest() == module.BLANK_CONFIG_SHA256
    assert hashlib.sha256(EMPTY_PROMPT.encode()).hexdigest() == module.BLANK_PROMPT_SHA256
    try:
        import tomllib
    except ImportError:
        import tomli as tomllib
    config = tomllib.loads(EMPTY_CONFIG)
    assert config['bot']['tool_approval_mode'] == 'workspace-write'
    assert config['sandbox']['bash'] == 'enforce'
    assert config['secrets']['protect_sensitive_files'] is True


def test_host_inspection_only_accepts_complete_private_empty_scaffold(tmp_path):
    ident = 'f' * 32
    root = tmp_path / 'instances'
    root.mkdir(mode=0o700)
    directory = root / ident
    directory.mkdir(mode=0o700)
    for name in ('home', 'home/prompts', 'workspace', 'state'):
        (directory / name).mkdir(mode=0o700)
    data = {'instance.json': json.dumps({'version': 1, 'instance_id': ident, 'status': 'prepared', 'running': False}),
            'native.token': 'x' * 48, 'home/config.toml': '', 'home/prompts/assistant.md': ''}
    for name, content in data.items():
        path = directory / name
        path.write_text(content)
        path.chmod(0o600)
    assert module.inspect_empty_scaffold(root, ident, os.getuid()) == directory
    extra = directory / 'home' / 'unexpected'
    extra.symlink_to(directory / 'native.token')
    with pytest.raises(ValueError):
        module.inspect_empty_scaffold(root, ident, os.getuid())
    extra.unlink()
    (directory / 'native.token').chmod(0o644)
    with pytest.raises(ValueError):
        module.inspect_empty_scaffold(root, ident, os.getuid())


@pytest.mark.skipif(os.geteuid() != 0, reason='root-owned fixture required; run in an isolated container')
def test_host_preparation_creates_stopped_service_only(tmp_path, monkeypatch):
    from types import SimpleNamespace
    import hashlib
    root = tmp_path / 'instances'
    agent = tmp_path / 'agent'
    units = tmp_path / 'units'
    root.mkdir(mode=0o711)
    agent.mkdir(mode=0o755)
    units.mkdir()
    for filename in ('reasonix', 'bridge', 'instance_supervisor.py', 'mailcheck.py', 'mail_transport.py', 'blank-config.toml', 'blank-prompt.md'):
        (agent / filename).write_text('fixture')
    digest = hashlib.sha256(b'fixture').hexdigest()
    monkeypatch.setattr(module, 'BLANK_CONFIG_SHA256', digest)
    monkeypatch.setattr(module, 'BLANK_PROMPT_SHA256', digest)
    real_path = Path
    mapping = {'/var/lib/asrobot-instances': root, '/opt/asrobot-agent': agent, '/etc/systemd/system': units}
    monkeypatch.setattr(module, 'Path', lambda value: mapping.get(str(value), real_path(value)))
    calls = []
    monkeypatch.setattr(module.subprocess, 'run', lambda args, **kw: calls.append(args))
    monkeypatch.setattr(module.os, 'chown', lambda *args, **kw: None)
    def account(name):
        if not any('useradd' in call[0] for call in calls):
            raise KeyError(name)
        return SimpleNamespace(pw_uid=12345, pw_gid=12345)
    monkeypatch.setattr(module.pwd, 'getpwnam', account)
    ident = 'b' * 32
    (agent / 'blank-config.toml').write_text('not-the-reviewed-template')
    with pytest.raises(ValueError, match='reviewed version'):
        module.prepare_empty_service(ident, '8.8.8.8')
    assert calls == [] and not (root / ident).exists()
    (agent / 'blank-config.toml').write_text('fixture')
    result = module.prepare_empty_service(ident, '8.8.8.8')
    assert result['state'] == 'prepared-and-stopped'
    assert (units / result['service']).is_file()
    assert (root / ident / 'native.token').stat().st_mode & 0o077 == 0
    assert all('start' not in call and 'enable' not in call for call in calls)
    with pytest.raises(ValueError, match='already exists'):
        module.prepare_empty_service(ident, '8.8.8.8')
