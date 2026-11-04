"""Read official calendars/notices, validate dates, atomically refresh an owner calendar.

No model, message delivery, subprocess, credentials, or executable website content.
Failures retain the last valid calendar; unverified future adjustments stay provisional.
"""
import argparse, copy, datetime as dt, hashlib, io, json, os, re, tempfile, urllib.parse, urllib.request, zipfile
from html.parser import HTMLParser
from pathlib import Path
from xml.etree import ElementTree as ET

SH=dt.timezone(dt.timedelta(hours=8))
SCHOOL_LIST='https://www.shiep.edu.cn/notice_student/list.htm'
CALENDAR_LIST='https://xxgk.shiep.edu.cn/502/list.htm'
GOV_LIST='https://sousuo.www.gov.cn/search-gov/data?'+urllib.parse.urlencode(dict(t='zhengcelibrary_gw',q='节假日安排的通知',sort='pubtime',searchfield='title',p=1,n=10))
ALLOWED={'www.shiep.edu.cn','shiep.edu.cn','xxgk.shiep.edu.cn','jwc.shiep.edu.cn','www.gov.cn','sousuo.www.gov.cn','www.beijing.gov.cn'}
class Document(HTMLParser):
    def __init__(self,text):
        super().__init__();self.parts=[];self.links=[];self.current=None;self.skip=0;self.feed(text)
    def handle_starttag(self,tag,attrs):
        a=dict(attrs)
        if tag in ('script','style'):self.skip+=1
        if tag=='a':self.current=[a.get('href',''),'']
    def handle_endtag(self,tag):
        if tag in ('script','style'):self.skip=max(0,self.skip-1)
        if tag=='a' and self.current:self.links.append(tuple(self.current));self.current=None
        if tag in ('p','div','br','li'):self.parts.append('\n')
    def handle_data(self,data):
        if self.skip:return
        self.parts.append(data)
        if self.current:self.current[1]+=data
    @property
    def text(self):return re.sub(r'[ \t\r\xa0]+',' ',''.join(self.parts))
def approved(url):
    u=urllib.parse.urlparse(url)
    if u.scheme!='https' or u.hostname not in ALLOWED or u.username or u.password:raise ValueError('unapproved calendar source')
    return url
class Redirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,req,fp,code,msg,headers,newurl):
        approved(newurl);return super().redirect_request(req,fp,code,msg,headers,newurl)
def fetch(url):
    approved(url)
    req=urllib.request.Request(url,headers={'User-Agent':'ASRobot-Calendar/1.0 (+read-only official calendar sync)'})
    with urllib.request.build_opener(Redirect()).open(req,timeout=12) as r:
        approved(r.url);data=r.read(4_000_001)
        if len(data)>4_000_000:raise ValueError('calendar response too large')
        return data

def xlsx_terms(data):
    ns={'m':'http://schemas.openxmlformats.org/spreadsheetml/2006/main'}
    with zipfile.ZipFile(io.BytesIO(data)) as z:
        if sum(i.file_size for i in z.infolist())>20_000_000:raise ValueError('calendar archive too large')
        strings=[''.join(n.itertext()) for n in ET.fromstring(z.read('xl/sharedStrings.xml')).findall('m:si',ns)] if 'xl/sharedStrings.xml' in z.namelist() else []
        values=[]
        for name in z.namelist():
            if not re.fullmatch(r'xl/worksheets/sheet\d+\.xml',name):continue
            for c in ET.fromstring(z.read(name)).findall('.//m:c',ns):
                v=c.find('m:v',ns)
                if c.get('t')=='s' and v is not None:values.append(strings[int(v.text)])
                elif c.get('t')=='inlineStr':values.append(''.join(c.itertext()))
    terms=[]
    for value in values:
        m=re.search(r'(上学期|下学期)\s*(20\d{2})年(\d+)月(\d+)日\s*至\s*(20\d{2})年(\d+)月(\d+)日',value)
        if m:
            start=dt.date(*map(int,m.group(2,3,4))).isoformat();end=dt.date(*map(int,m.group(5,6,7))).isoformat()
            academic_year=int(m.group(2))-(int(m.group(3))<8)
            terms.append({'name':f'{academic_year}—{academic_year+1}学年'+m.group(1),'start':start,'end':end})
    if len(terms)!=2:raise ValueError('expected two explicit semester date ranges')
    return terms

def clean_notice(text):return re.sub(r'\s+','',re.sub(r'[（(][^）)]*[）)]','',text))
RANGE=re.compile(r'(?:(20\d{2})年)?(\d{1,2})月(\d{1,2})日(?:至(?:(20\d{2})年)?(?:(\d{1,2})月)?(\d{1,2})日)?放假')
def notice(text,year,school=False):
    text=clean_notice(text);closures=[];makeups={}
    for m in RANGE.finditer(text):
        y=int(m[1] or year);month=int(m[2]);day=int(m[3]);endmonth=int(m[5] or month);endday=int(m[6] or day)
        if not m[1] and endmonth<month:y=year-1
        endyear=int(m[4] or y+(endmonth<month));start=dt.date(y,month,day);end=dt.date(endyear,endmonth,endday)
        if not 0<=(end-start).days<=20:raise ValueError('unexpected holiday range')
        closures.append({'start':start.isoformat(),'end':end.isoformat(),'reason':'学校正式放假安排' if school else '国家放假调休安排'})
    for m in re.finditer(r'(\d{1,2})月(\d{1,2})日上原计划(\d{1,2})月(\d{1,2})日',text):
        actual=dt.date(year,int(m[1]),int(m[2])).isoformat();original=dt.date(year,int(m[3]),int(m[4])).isoformat()
        makeups[actual]={'teaching':True,'class_date':original,'reason':'学校调课：补'+original+'的课程'}
    if not school:
        for sentence in re.split('[。；]',text):
            if not re.search(r'上班|上课',sentence) or '放假' in sentence:continue
            for m in re.finditer(r'(?:(20\d{2})年)?(\d{1,2})月(\d{1,2})日',sentence):
                date=dt.date(int(m[1] or year),int(m[2]),int(m[3])).isoformat()
                makeups[date]={'teaching':True,'reason':'国家调休工作日，具体课程对应日期以学校通知为准'}
        if len(closures)<7:raise ValueError('national notice is incomplete; keep last valid year')
    elif not closures:raise ValueError('school notice has no explicit holiday ranges')
    return {'year':year,'school':school,'closures':closures,'makeups':makeups}

def days(start,end):
    d=dt.date.fromisoformat(start);last=dt.date.fromisoformat(end)
    while d<=last:yield d.isoformat();d+=dt.timedelta(days=1)
def compile_calendar(seed,documents,terms):
    c=copy.deepcopy(seed);covered={str(d['year']) for d in documents if not d['school']};c['closures']=[r for r in c['closures'] if not ('待核实' in r['reason'] and r['start'][:4] in covered)];merged={(t['start'][:4],int(t['start'][5:7])>=8):t for t in c['terms']}
    merged.update({(t['start'][:4],int(t['start'][5:7])>=8):t for t in terms});c['terms']=sorted(merged.values(),key=lambda t:t['start'])[-8:]
    c['valid_from']=min(c['valid_from'],min(t['start'] for t in c['terms']))
    maxend=dt.date.fromisoformat(max(t['end'] for t in c['terms']));c['valid_to']=max(c['valid_to'],dt.date(maxend.year,8,31).isoformat())
    # National dates are the baseline; explicit school closures/mappings win.
    for doc in sorted(documents,key=lambda d:(bool(d['school']),d.get('published_at',''),int((re.search(r'a(\d+)/page',d['url']) or [None,'0'])[1]))):
        url=doc['url']
        if not doc['school']:c['holiday_years'][str(doc['year'])]=True
        for closure in doc['closures']:
            closure={**closure,'source':url};c['closures'].append(closure)
            if doc['school']:
                for date in days(closure['start'],closure['end']):c['adjustments'][date]={'teaching':False,'reason':closure['reason'],'source':url}
        for date,a in doc['makeups'].items():
            # School-specific seed records cannot be replaced by a national workday.
            prior=c['adjustments'].get(date,{})
            if not doc['school'] and 'shiep.edu.cn' in prior.get('source',''):continue
            c['adjustments'][date]={**a,'source':url}
    for doc in documents:
        if not any(x['url']==doc['url'] for x in c['sources']):c['sources'].append({'title':doc['title'],'url':doc['url']})
    unique={(r['start'],r['end'],r.get('source','')):r for r in c['closures']};c['closures']=list(unique.values())
    c['updated_at']=dt.datetime.now(SH).isoformat(timespec='seconds')
    c['revision']=hashlib.sha256(json.dumps({k:v for k,v in c.items() if k not in ('revision','updated_at')},sort_keys=True,ensure_ascii=False).encode()).hexdigest()[:20]
    validate(c);return c

def validate(c):
    start,end=dt.date.fromisoformat(c['valid_from']),dt.date.fromisoformat(c['valid_to'])
    if c['version']!=1 or start>end or not 1<=len(c['terms'])<=8:raise ValueError('invalid calendar')
    for t in c['terms']:
        a,b=dt.date.fromisoformat(t['start']),dt.date.fromisoformat(t['end'])
        if not start<=a<=b<=end or (b-a).days>200:raise ValueError('invalid semester')
    if len(c['adjustments'])>1000 or len(c['closures'])>200:raise ValueError('calendar capacity exceeded')
    for date,a in c['adjustments'].items():
        dt.date.fromisoformat(date)
        if a.get('class_date'):dt.date.fromisoformat(a['class_date'])
def atomic(path,value):
    fd,name=tempfile.mkstemp(prefix='.academic-',dir=path.parent)
    try:
        os.fchmod(fd,0o600)
        with os.fdopen(fd,'w') as f:json.dump(value,f,ensure_ascii=False,indent=2);f.flush();os.fsync(f.fileno())
        os.replace(name,path)
    finally:
        if os.path.exists(name):os.unlink(name)
def run(home,offline=False):
    folder=Path(home)/'assistant';folder.mkdir(mode=0o700,parents=True,exist_ok=True)
    current=folder/'academic-calendar.json';seed=json.loads((folder/'academic-calendar-seed.json').read_text());validate(seed)
    cachepath=folder/'academic-source-cache.json';cache=json.loads(cachepath.read_text()) if cachepath.exists() else {'documents':{},'terms':[]}
    errors=[];checked=[]
    if not offline:
        minimum_year=int(seed['valid_from'][:4])
        for listing,kind in [(CALENDAR_LIST,'calendar'),(SCHOOL_LIST,'school'),(GOV_LIST,'national')]:
            try:
                raw=fetch(listing);checked.append(listing)
                if kind=='national':
                    data=json.loads(raw)
                    if data.get('code')!=200:raise ValueError('government search unavailable')
                    links=[(r['url'],Document(r['title']).text) for r in data.get('searchVO',{}).get('listVO',[])]
                else:
                    links=Document(raw.decode('utf-8','replace')).links
                selected=[]
                for href,title in links:
                    title=''.join(title.split());url=urllib.parse.urljoin(listing,href)
                    year_match=re.search(r'20\d{2}',title)
                    if not year_match or int(year_match[0])<minimum_year:continue
                    if kind=='calendar' and '学年' in title and '校历' in title and url.endswith('.xlsx'):selected.append((url,title))
                    elif kind=='school' and re.search(r'20\d{2}年.*(?:放假|假期).*通知',title) and not any(x in title for x in ['教职工','研究生','新生']):selected.append((url,title))
                    elif kind=='national' and re.fullmatch(r'国务院办公厅关于20\d{2}年部分节假日安排的通知',title):selected.append((url,title))
                for url,title in selected[:4]:
                    try:
                        raw=fetch(url)
                        if kind=='calendar':
                            parsed=xlsx_terms(raw);existing={(t['start'][:4],int(t['start'][5:7])>=8):t for t in cache['terms']};existing.update({(t['start'][:4],int(t['start'][5:7])>=8):t for t in parsed});cache['terms']=list(existing.values());continue
                        year=int(re.search(r'20\d{2}',title)[0]);doc=notice(Document(raw.decode('utf-8','replace')).text,year,kind=='school');doc.update(url=url,title=title,sha256=hashlib.sha256(raw).hexdigest());cache['documents'][url]=doc
                    except Exception as e:errors.append({'source':url,'message':type(e).__name__+': '+str(e)[:180]})
            except Exception as e:errors.append({'source':listing,'message':type(e).__name__+': '+str(e)[:180]})
    calendar=compile_calendar(seed,list(cache['documents'].values()),cache['terms'])
    atomic(cachepath,cache);atomic(current,calendar)
    status={'checked_at':dt.datetime.now(SH).isoformat(timespec='seconds'),'offline':offline,'checked_sources':checked,'errors':errors,'pending_national_years':[y for y,ok in calendar['holiday_years'].items() if not ok],'calendar_revision':calendar['revision']};atomic(folder/'academic-sync-status.json',status)
    return status
if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--home',required=True);p.add_argument('--offline',action='store_true');args=p.parse_args()
    # Prevent concurrent timer/manual refreshes from losing a cached source.
    import fcntl
    parent=Path(args.home)/'assistant';parent.mkdir(parents=True,exist_ok=True,mode=0o700)
    with open(parent/'.academic-sync.lock','a') as lock:fcntl.flock(lock,fcntl.LOCK_EX);print(json.dumps(run(args.home,args.offline),ensure_ascii=False))
