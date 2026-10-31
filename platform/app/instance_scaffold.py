"""Prepare empty, private instance storage; never starts or attaches a runtime."""
import json
import os
import re
import secrets
import stat
from pathlib import Path
from . import store as s

EMPTY_CONFIG = '''default_model = "openai/needs-configuration"
[agent]
system_prompt_file = "prompts/assistant.md"
[sandbox]
bash = "enforce"
network = true
[secrets]
filter_subprocess_env = true
protect_sensitive_files = true
[bot]
enabled = false
tool_approval_mode = "workspace-write"
[bot.allowlist]
enabled = true
allow_all = false
weixin_users = []
[[providers]]
name = "openai"
kind = "openai"
base_url = "https://api.openai.com/v1"
api_key_env = "ASROBOT_MODEL_API_KEY"
models = ["needs-configuration"]
default = "needs-configuration"
[[providers]]
name = "qianwen_maas"
kind = "openai"
base_url = "https://maas.qianwenaiapi.com/compatible-mode/v1"
api_key_env = "ASROBOT_QIANWEN_API_KEY"
models = ["needs-configuration"]
default = "needs-configuration"
[[providers]]
name = "dashscope"
kind = "openai"
base_url = "https://dashscope.aliyuncs.com/compatible-mode/v1"
api_key_env = "ASROBOT_DASHSCOPE_API_KEY"
models = ["needs-configuration"]
default = "needs-configuration"
[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://api.deepseek.com/v1"
api_key_env = "ASROBOT_DEEPSEEK_API_KEY"
models = ["needs-configuration"]
default = "needs-configuration"
'''
EMPTY_PROMPT = '你是用户的私人助理。尊重用户为你设置的名称和称呼，根据实际功能与数据回答。\n'

def _write_private(path, content):
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, 'O_NOFOLLOW', 0)
    fd = os.open(path, flags, 0o600)
    with os.fdopen(fd, 'w') as out:
        out.write(content)


def prepare(user_id, root):
    """Create storage for an already-reserved, inactive instance.

    The root is supplied by the trusted operator, not by an HTTP request.
    Existing instance directories are never overwritten or reused implicitly.
    """
    root = Path(root)
    info = root.lstat()
    # Traverse-only access lets distinct per-instance OS users reach their own
    # directory while preventing them from listing or modifying the root.
    if (not stat.S_ISDIR(info.st_mode) or root.is_symlink() or
            info.st_uid != os.getuid() or info.st_mode & 0o066):
        raise ValueError('instance root must be a private, non-symbolic directory')
    with s.db() as db:
        row = db.execute("SELECT id FROM assistant_instances WHERE user_id=? AND state='reserved'", (user_id,)).fetchone()
    if not row or not re.fullmatch(r'[0-9a-f]{32}', row['id']):
        raise ValueError('reserved instance required')
    instance_id = row['id']
    directory = root / instance_id
    directory.mkdir(mode=0o700)
    for name in ('home', 'workspace', 'state'):
        (directory / name).mkdir(mode=0o700)
    (directory / 'home' / 'prompts').mkdir(mode=0o700)
    _write_private(directory / 'home' / 'config.toml', EMPTY_CONFIG)
    _write_private(directory / 'home' / 'prompts' / 'assistant.md', EMPTY_PROMPT)
    _write_private(directory / 'native.token', secrets.token_urlsafe(48))
    manifest = {'version': 1, 'instance_id': instance_id, 'status': 'prepared', 'running': False}
    _write_private(directory / 'instance.json', json.dumps(manifest))
    return instance_id
