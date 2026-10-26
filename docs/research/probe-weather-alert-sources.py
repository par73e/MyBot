import urllib.request,urllib.parse,concurrent.futures,time,json,hashlib,re,datetime,os
items=[
 ('shanghai_jsonp','https://d1.weather.com.cn/shweather/alarm.json','https://sh.weather.com.cn/zhyj/'),
 ('national_map','https://forecast.weather.com.cn/api/v1/traffic/alarm/alarmMap?callback=getAlarm','https://www.weather.com.cn/alarm/'),
 ('national_fallback','https://product.weather.com.cn/alarm/grepalarm_cn.php','https://www.weather.com.cn/alarm/'),
 ('nmc_all','https://www.nmc.cn/rest/findAlarm?pageNo=1&pageSize=20','https://www.nmc.cn/f/alarm.html'),
 ('nmc_shanghai','https://www.nmc.cn/rest/findAlarm?'+urllib.parse.urlencode({'pageNo':1,'pageSize':20,'signaltype':'','signallevel':'','province':'上海市'}),'https://www.nmc.cn/f/alarm.html'),
 ('shanghai_cma','http://smb.shweather.cn:5678/smb/Home/GetAlertListByType?type=sh','http://sh.cma.gov.cn/sh/tqyb/zhyj/'),
 ('national_12379','https://www.12379.cn/',''),
]
def get(it):
 name,url,ref=it;start=time.monotonic();out={'name':name,'url':url,'checked_at':datetime.datetime.now(datetime.timezone.utc).isoformat()}
 try:
  h={'User-Agent':'Mozilla/5.0'}
  if ref:h['Referer']=ref
  with urllib.request.urlopen(urllib.request.Request(url,headers=h),timeout=12) as r:
   raw=r.read(1500001);out.update(status=r.status,bytes=len(raw),seconds=round(time.monotonic()-start,3),final_url=r.url,headers={k:v for k,v in r.headers.items() if k.lower() in ['content-type','date','age','cache-control','last-modified','etag','expires']},sha256=hashlib.sha256(raw).hexdigest())
  if len(raw)>1500000:raise ValueError('body exceeds probe limit')
  text=raw.decode('utf-8-sig','replace').strip();data=None
  try:data=json.loads(text)
  except ValueError:
   m=re.fullmatch(r'(?:alarm_callback|getAlarm)\s*\((.*)\)\s*;?',text,re.S)
   if m:data=json.loads(m[1])
   else:
    m=re.fullmatch(r'(?:var\s+)?alarminfo\s*=\s*(.*?);?',text,re.S)
    if m:data=json.loads(m[1])
  out['valid_data']=data is not None
  if isinstance(data,list):out.update(data_type='array',count=len(data),sample=data[:2])
  elif isinstance(data,dict):
   out.update(data_type='object',keys=list(data),sample={k:v for k,v in data.items() if not isinstance(v,(dict,list))})
   if name.startswith('nmc'):
    page=data.get('data',{}).get('page',{});out.update(count=page.get('count'),page={k:v for k,v in page.items() if k!='list'},sample_items=page.get('list',[])[:2])
   elif name=='national_map':
    r=data.get('result',{});out['result_type']=type(r).__name__;out['result_keys']=list(r) if isinstance(r,dict) else None
    if isinstance(r,dict):out['result_summary']={k:(len(v) if isinstance(v,list) else v) for k,v in r.items() if k!='data'};out['sample_items']=r.get('data',[])[:2];out['shanghai_items']=[v for v in r.get('data',[]) if '上海' in str(v)]
   elif name=='national_fallback':out['count']=data.get('count');out['sample_items']=data.get('data',[])[:2];out['shanghai_items']=[v for v in data.get('data',[]) if '上海' in str(v)]
  else:out['preview']=text[:120]
  if os.environ.get('PROBE_RAW_DIR'):
   from pathlib import Path
   Path(os.environ['PROBE_RAW_DIR'],name+'.raw').write_bytes(raw)
 except Exception as e:out.update(error=str(e),seconds=round(time.monotonic()-start,3))
 return out
print(json.dumps({'results':list(concurrent.futures.ThreadPoolExecutor(max_workers=4).map(get,items))},ensure_ascii=False,indent=2))
