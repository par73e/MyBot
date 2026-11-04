import copy
import json
from pathlib import Path
import pytest
from deploy import academic_sync as sync

SEED=json.loads((Path(__file__).parents[1]/'deploy/academic_calendar_seed.json').read_text())
NATIONAL='''国务院办公厅关于2027年部分节假日安排的通知
1月1日至3日放假。1月4日上班。
2月5日至12日放假。2月13日上班。
4月3日至5日放假。5月1日至5日放假。6月9日放假。
9月15日至17日放假。10月1日至7日放假。10月9日上班。'''
# Synthetic future dates exercise parsing; they are never shipped as real rules.
def test_notice_school_priority_and_future_status():
    national=sync.notice(NATIONAL,2027);national.update(url='https://www.gov.cn/test',title='synthetic test')
    school=sync.notice('2027年元旦：1月1日至4日放假。1月9日上原计划1月4日的课程。',2027,True);school.update(url='https://www.shiep.edu.cn/test',title='synthetic test')
    c=sync.compile_calendar(SEED,[school,national],[])
    assert c['holiday_years']['2027'] is True
    assert not c['adjustments']['2027-01-04']['teaching']
    assert c['adjustments']['2027-01-09']['class_date']=='2027-01-04'
    assert c['adjustments']['2026-09-20']['class_date']=='2026-10-06'
    assert not any('待核实' in r['reason'] and r['start'].startswith('2027') for r in c['closures'])
    assert SEED['holiday_years']['2027'] is False

def test_partial_year_and_unapproved_sources_rejected():
    with pytest.raises(ValueError):sync.notice('1月1日至3日放假。',2027)
    for url in ['http://www.gov.cn/a','https://127.0.0.1/a','https://www.gov.cn.attacker.test/a','https://user@www.gov.cn/a']:
        with pytest.raises(ValueError):sync.approved(url)

def test_failed_fetch_retains_last_valid_rules_and_marks_update_failure(tmp_path,monkeypatch):
    folder=tmp_path/'assistant';folder.mkdir();(folder/'academic-calendar-seed.json').write_text(json.dumps(SEED))
    sync.run(tmp_path,True);before=json.loads((folder/'academic-calendar.json').read_text())
    monkeypatch.setattr(sync,'fetch',lambda url:(_ for _ in ()).throw(TimeoutError('offline')))
    result=sync.run(tmp_path)
    after=json.loads((folder/'academic-calendar.json').read_text())
    assert before['revision']==after['revision']
    assert len(result['errors'])==3
    assert result['pending_national_years']==['2027']
    assert (folder/'academic-calendar.json').stat().st_mode&0o777==0o600

def test_updated_term_replaces_old_dates_instead_of_overlapping():
    revised=copy.deepcopy(SEED['terms'][0]);revised['start']='2026-09-15'
    c=sync.compile_calendar(SEED,[],[revised])
    assert len(c['terms'])==2
    assert c['terms'][0]['start']=='2026-09-15'

def test_national_discovery_uses_official_search_results(tmp_path,monkeypatch):
    folder=tmp_path/'assistant';folder.mkdir();(folder/'academic-calendar-seed.json').write_text(json.dumps(SEED))
    url='https://www.gov.cn/zhengce/test2027.htm'
    def fetch(source):
        if source==sync.GOV_LIST:return json.dumps({'code':200,'searchVO':{'listVO':[{'url':url,'title':'国务院办公厅关于2027年部分<em>节假日</em>安排的通知'}]}}).encode()
        if source==url:return NATIONAL.encode()
        return b'<html></html>'
    monkeypatch.setattr(sync,'fetch',fetch)
    assert not sync.run(tmp_path)['pending_national_years']
    c=json.loads((folder/'academic-calendar.json').read_text())
    assert c['holiday_years']['2027'] is True
    assert c['adjustments']['2027-01-04']['teaching']
