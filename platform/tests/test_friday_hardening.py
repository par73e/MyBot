import tomllib
import pytest
from deploy.prepare_friday_hardening import PROTECTED, prepare


def test_hardening_preserves_unrelated_config_and_existing_read_denies():
    before='''[bot]\ntool_approval_mode = "workspace-write"\n[sandbox]\nnetwork = true\nforbid_read = ["/existing"]\n[secrets]\nfilter_subprocess_env = false\nprotect_sensitive_files = false\n[provider]\nmodel = "retained"\n'''
    after=tomllib.loads(prepare(before))
    assert after['bot']['tool_approval_mode']=='workspace-write'
    assert after['sandbox']['network'] is True
    assert after['sandbox']['forbid_read']==['/existing']+PROTECTED
    assert after['secrets']=={'filter_subprocess_env':True,'protect_sensitive_files':True}
    assert after['provider']['model']=='retained'


def test_missing_sections_created_and_reapplication_idempotent():
    prepared=prepare('[bot]\ntool_approval_mode = "workspace-write"\n')
    assert prepare(prepared)==prepared
    assert tomllib.loads(prepared)['sandbox']['forbid_read']==PROTECTED


def test_ambiguous_multiline_array_is_not_rewritten():
    with pytest.raises(ValueError,match='multiline'):
        prepare('[sandbox]\nforbid_read = [\n "/existing",\n]\n')


def test_array_tables_are_not_accidentally_modified():
    text='[secrets]\nfilter_subprocess_env = false\n[[providers]]\nname = "example"\n[sandbox]\nnetwork = true\n'
    result=tomllib.loads(prepare(text))
    assert result['providers']==[{'name':'example'}]
    assert result['secrets']['filter_subprocess_env'] is True
    assert result['sandbox']['network'] is True
