#!/usr/bin/env python3
"""Evidence-only 8/71 weight controller. The second algorithm may honestly abstain.
No probing, application config edits, priority/status writes, or scheduling API.
"""
import argparse,copy,datetime as dt,fcntl,hashlib,json,math,os,statistics,subprocess,sys,tempfile
from pathlib import Path
import weight_sql as w
ROOT=Path(__file__).resolve().parent
HOME=Path('/workspace/ops/channel-weight-only')
KRL=Path('/workspace/ops/krl26-gradual-recovery-20261124/STATE.json')
CONFIGS=[Path('/workspace/reports/ops')/d/'config.json' for d in ['channel-autoweight-go','channel-autoweight-claude']]
SSH=['ssh','-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','ConnectTimeout=15','reverixai-personal-site','docker','exec','-i','new-api-postgres','psql','-X','-qAt','-U','newapi','-d','newapi','-v','ON_ERROR_STOP=1']
ZONE=dt.timezone(dt.timedelta(hours=8))
def now():return dt.datetime.now(ZONE).isoformat()
def digest(p):return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def save(p,x):
 p=Path(p);body=x if isinstance(x,str) else json.dumps(x,ensure_ascii=False,indent=2,allow_nan=False)
 fd=os.open(p,os.O_CREAT|os.O_EXCL|os.O_WRONLY,0o600)
 with os.fdopen(fd,'w') as f:f.write(body);f.flush();os.fsync(f.fileno())
def atomic(p,x):
 p=Path(p);fd,name=tempfile.mkstemp(dir=p.parent,prefix='.rb8-')
 try:
  with os.fdopen(fd,'w') as f:os.fchmod(f.fileno(),0o600);json.dump(x,f,ensure_ascii=False,indent=2,allow_nan=False);f.flush();os.fsync(f.fileno())
  os.replace(name,p);fd=os.open(p.parent,os.O_RDONLY);os.fsync(fd);os.close(fd)
 finally:
  if os.path.exists(name):os.unlink(name)
def db(sql,folder,label):
 r=subprocess.run(SSH,input=sql,capture_output=True,text=True,timeout=90);save(folder/(label+'.stderr.txt'),r.stderr)
 if r.returncode:raise RuntimeError('Database operation failed; inspect private evidence '+str(folder))
 return r.stdout
READ="BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SET LOCAL statement_timeout='30s'; "+w.QUERY+'; ROLLBACK;'
def export_sql():
 # Only selected telemetry exported; no prompt, content, API key or raw other leaves production.
 return """BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SET LOCAL statement_timeout='30s';
WITH raw AS (
 SELECT id,created_at,type,channel_id,model_name,use_time,is_stream,coalesce(completion_tokens,0) ct,coalesce(prompt_tokens,0) pt,
 md5(coalesce(nullif(btrim(request_id),''),'missing:'||id::text)) rid,coalesce(btrim(request_id),'')<>'' has_rid,
 md5(token_id::text) tok,
 CASE WHEN other ~ '^\\s*\\{' THEN other::jsonb ELSE '{}'::jsonb END oj,
 CASE WHEN type=5 AND (content ILIKE '%currently busy%' OR content ILIKE '%server is busy%' OR content ILIKE '%overloaded%' OR content ILIKE '%timed out%' OR content ILIKE '%Concurrency limit exceeded%' OR content ILIKE '%Model service temporarily limited%' OR content ~ 'status_code=(408|429|5[0-9][0-9])') THEN 'channel'
 WHEN type=5 AND (content ILIKE '%client_gone%' OR content ILIKE '%Client connection closed%' OR content ILIKE '%cancel%' OR content ~ 'status_code=4[0-9][0-9]') THEN 'client'
 WHEN type=5 THEN 'unknown' ELSE NULL END error_class
 FROM logs WHERE channel_id IN (2,23,26,32,67,68,72,73) AND type IN (2,5)
 AND created_at>=extract(epoch FROM now())::bigint-7200 AND created_at<extract(epoch FROM now())::bigint
 AND coalesce(token_id,0) NOT IN (0,38,1305,1318)
), safe AS (
 SELECT id,created_at,type,channel_id,model_name,use_time,is_stream,ct,pt,rid,has_rid,tok,error_class,
 CASE WHEN (oj->>'frt_version')='2' AND (oj->>'frt') ~ '^[0-9]+(\\.[0-9]+)?$' THEN (oj->>'frt')::float/1000.0 END ttft,
 oj->'stream_status'->>'end_reason' end_reason,
 (oj->>'content_timing_measured') IN ('true','t') timing_measured,
 (oj->>'frt_version')='2' frt_v2,
 CASE WHEN (oj->>'content_chunks') ~ '^[0-9]+$' THEN (oj->>'content_chunks')::int END chunks,
 (EXISTS(SELECT 1 FROM jsonb_object_keys(oj) k WHERE k ~* '(tool|function_call|structured)')) toolish
 FROM raw
)
SELECT jsonb_build_object('cutoff',extract(epoch FROM now())::bigint,'checked_at',now(),
 'snapshot',("""+w.QUERY+"""),
 'schema',jsonb_build_object('weights',(SELECT jsonb_agg(jsonb_build_object('table',table_name,'type',udt_name)) FROM information_schema.columns WHERE table_schema='public' AND table_name IN ('channels','abilities') AND column_name='weight'),
 'constraints',(SELECT jsonb_agg(jsonb_build_object('table',conrelid::regclass::text,'definition',pg_get_constraintdef(oid))) FROM pg_constraint WHERE conrelid IN ('public.channels'::regclass,'public.abilities'::regclass)),
 'custom_triggers',(SELECT count(*) FROM pg_trigger WHERE tgrelid IN ('public.channels'::regclass,'public.abilities'::regclass) AND NOT tgisinternal),
 'log_columns',(SELECT jsonb_agg(column_name ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='public' AND table_name='logs')),
 'rows',coalesce((SELECT jsonb_agg(to_jsonb(s) ORDER BY created_at,id) FROM safe s),'[]'::jsonb)); ROLLBACK;
"""
def percentile(v,q):
 if not v:return None
 v=sorted(v);at=(len(v)-1)*q;i=int(at);return v[i]+(v[min(i+1,len(v)-1)]-v[i])*(at-i)
def finite(v):return type(v) in (float,int) and math.isfinite(v)
def normalized(v):
 if not finite(v):raise ValueError('non-finite/missing normalization')
 return round(max(0.,min(1.,v)),3)
def mapped(v):return None if v is None else int(math.floor(normalized(v)*50+0.5))
def classify(r,attempts):
 if not r['has_rid']:return 'unknown'
 if r['type']==5:return {'channel':'channel_fault','client':'client'}.get(r['error_class'],'unknown')
 if r['ct']>0:return 'success' if attempts==1 else 'retry_not_original'
 if r.get('end_reason') in ('client_gone','client_cancel','cancelled','canceled'):return 'client'
 if r.get('toolish'):return 'unknown'
 if r.get('frt_v2') and r.get('timing_measured') and r.get('chunks')==0 and r['pt']>0 and r.get('end_reason') in ('eof','complete','stop',''):return 'explicit_empty'
 return 'unknown'
def dedup(rows):
 # Preserve each error-vs-consume type separately; never erase a failed channel via retry success.
 counts={};latest={}
 for r in rows:
  key=(r['channel_id'],r['rid']);counts[key]=counts.get(key,0)+1
  k=key+(r['type'],)
  if k not in latest or (r['created_at'],r['id'])>(latest[k]['created_at'],latest[k]['id']):latest[k]=r
 out=[]
 for r in latest.values():
  x=dict(r);x['class']=classify(x,counts[(x['channel_id'],x['rid'])]);out.append(x)
 return out

def metrics(rows,cutoff,ids):
 result={}
 for cid in ids:
  rr=[r for r in rows if r['channel_id']==cid];ok=[r for r in rr if r['class']=='success']
  # Table: telemetry on nonempty consume observations, independently from first-attempt quality.
  consume=[r for r in rr if r['type']==2 and r['ct']>0]
  tt=[r['ttft'] for r in consume if finite(r.get('ttft')) and r['ttft']>0]
  ee=[r['use_time'] for r in consume if finite(r.get('use_time')) and r['use_time']>=0]
  speed=[r['ct']/(r['use_time']+0.5) for r in consume if r['ct']>=50 and r['use_time']>=3 and r['is_stream'] is not False]
  classes={k:sum(r['class']==k for r in rr) for k in ['success','channel_fault','client','explicit_empty','unknown','retry_not_original']}
  recent=[r for r in rr if r['created_at']>=cutoff-600]
  faults=classes['channel_fault']+classes['explicit_empty'];den=len(ok)+faults
  tokens={}
  for r in ok:tokens[r['tok']]=tokens.get(r['tok'],0)+1
  result[cid]={'n':len(rr),'error':sum(r['type']==5 for r in rr),'classes':classes,'success_n':len(ok),'ttft_n':len(tt),'ttft_p50':percentile(tt,.5),'ttft_p90':percentile(tt,.9),'e2e_n':len(ee),'e2e_p50':percentile(ee,.5),'e2e_p90':percentile(ee,.9),'tps_n':len(speed),'tps_mean':statistics.mean(speed) if speed else None,'faults':faults,'fault_den':den,'fault_rate':faults/den if den else None,'recent_faults':sum(r['class'] in ('channel_fault','explicit_empty') for r in recent),'tokens':len(tokens),'top_share':max(tokens.values())/sum(tokens.values()) if tokens else None}
 return result

def matched(rows,ids,min_n):
 # Exact-model/stream/output-band common support; descriptive, never causal first-pass evidence.
 cells={}
 for r in rows:
  if r['channel_id'] not in ids or r['class']!='success' or r['ct']<50 or r['use_time']<3:continue
  band=int(math.log2(max(1,r['ct']/50)));key=(r['model_name'],r['is_stream'],band)
  cells.setdefault(key,{}).setdefault(r['channel_id'],[]).append(r)
 ratios={cid:{'matched_e2e':[],'ttft':[],'e2e_tps':[]} for cid in ids};ns={cid:0 for cid in ids}
 for cell in cells.values():
  rich={cid:v for cid,v in cell.items() if len(v)>=min_n}
  if len(rich)<2:continue
  med={cid:statistics.median(r['use_time']*1000/r['ct'] for r in v) for cid,v in rich.items()}
  best=min(med.values())
  for cid,v in rich.items():
   if best>0:ratios[cid]['matched_e2e'].append(best/med[cid]);ns[cid]+=len(v)
  tts={cid:[r['ttft'] for r in v if finite(r.get('ttft')) and r['ttft']>0] for cid,v in rich.items()}
  tts={cid:v for cid,v in tts.items() if len(v)>=min_n}
  if len(tts)>=2:
   bt=min(statistics.median(v) for v in tts.values())
   for cid,v in tts.items():ratios[cid]['ttft'].append(bt/statistics.median(v))
  speeds={cid:statistics.mean(r['ct']/(r['use_time']+.5) for r in v) for cid,v in rich.items()}
  bs=max(speeds.values())
  if bs>0:
   for cid,v in speeds.items():ratios[cid]['e2e_tps'].append(v/bs)
 return {cid:{'quality':statistics.mean(ratios[cid]['matched_e2e']) if ratios[cid]['matched_e2e'] else None,'components':{k:statistics.mean(v) for k,v in ratios[cid].items() if v},'n':ns[cid],'cells':len(ratios[cid]['matched_e2e'])} for cid in ids}

def independent(ms,rows,current,policy,cutoff):
 target=dict(current);review={}
 for profile,ids in policy['profiles'].items():
  mm=matched(rows,ids,policy['min_common_stratum_n'])
  for cid in ids:
   m=ms[cid];components=mm[cid]['components'];reasons=[];old=current[cid]
   q=None;raw=None;final=old
   if m['success_n']>=policy['min_real_success'] and m['tokens']>=3 and m['top_share']<=0.5 and len(components)>=2 and mm[cid]['n']>=20:
    weights=policy['components'];q0=sum(weights[k]*v for k,v in components.items())/sum(weights[k] for k in components)
    conf=m['success_n']/(m['success_n']+policy['confidence_k'])
    # Fixed shrinkage toward previous normalized weight; no within-round min/max stretching.
    q=normalized(conf*q0+(1-conf)*old/50);raw=mapped(q)
    step=policy['max_performance_step'];final=max(old-step,min(old+step,raw));reasons.append('observational descriptive evidence, not causal algorithm score')
   else:reasons.append('sparse/confounded/missing matched data: hold')
   f=policy['fault'];severe=m['recent_faults']>=f['recent_min_count'] or (m['fault_den']>=f['severe_min_n'] and m['faults']>=f['severe_min_count'] and m['fault_rate']>=f['severe_rate'])
   if severe:final=min(final,30);reasons.append('channel-fault protection cap30')
   # Rescue/retry evidence may only block an increase. Unknown alone must not
   # (all GPT/CC; no channel-specific lock). Confirmed channel_fault and
   # explicit_empty still use the risk cap above. Authorized 2026-11-26 pt80u.
   if m['classes']['retry_not_original']:
    final=min(final,old);reasons.append('rescue/retry evidence blocks increases')
   # [KRL]-GPT-0.13 uses the same independent layer as other GPT channels
   # (map floor(50*q+.5), max ±6, rescue/retry may block increases, fault cap30).
   # The special pair/baseline rise lock was revoked 2026-11-26 (pt79u).
   # Unknown-alone increase block was revoked 2026-11-26 (pt80u).
   target[cid]=final
   review[cid]={'profile':profile,'independent_normalized_reference':q,'independent_mapping':raw,'components':components,'matched':mm[cid],'final_target':final,'reasons':reasons}
 return target,review

def krl_windows(rows,cutoff,source,prior=None):
 windows=copy.deepcopy(prior['krl_consumed_windows'] if prior else source['consumed_windows'])
 last=prior['krl_last_window_end'] if prior else source['last_consumed_window_end']
 start=int(dt.datetime.fromisoformat(last).timestamp());gap=None
 if start<cutoff-7200:
  gap={'from':last,'to':dt.datetime.fromtimestamp(cutoff-7200,ZONE).isoformat(),'reason':'missed telemetry interval, not healthy and not fabricated'}
  start+=max(0,math.ceil((cutoff-7200-start)/1800))*1800
 while start+1800<=cutoff:
  end=start+1800; rr=[r for r in rows if r['channel_id']==26 and start<=r['created_at']<end];m=metrics(rr,end,[26])[26]
  windows.append({'start':dt.datetime.fromtimestamp(start,ZONE).isoformat(),'end':dt.datetime.fromtimestamp(end,ZONE).isoformat(),'healthy':False,'success':m['success_n'],'channel_fault':m['classes']['channel_fault'],'explicit_empty':m['classes']['explicit_empty'],'unknown':m['classes']['unknown'],'ttft_n':m['ttft_n'],'ttft_p50_s':m['ttft_p50'],'ttft_p90_s':m['ttft_p90'],'tokens':m['tokens'],'max_token_share':m['top_share'],'pairs':None,'degradation_baseline':None,'observational_notes':['verified comparable pairs still unavailable','fault-period TTFT baseline still unidentifiable; not used to hold weight'],'source':'rb8 unified90m; window ledger observational only, does not gate weight after 2026-11-26 pt79u'})
  start=end;last=dt.datetime.fromtimestamp(end,ZONE).isoformat()
 return {'consumed_windows':windows,'last_consumed_window_end':last,'gap':gap,'healthy_streak':0,'special_rise_lock':False,'rise_allowed':True,'weight_policy':'same_independent_layer_as_other_gpt','policy_amendment_applied':'2026-11-26T20:14:00+08:00 pt80u: unknown alone no longer blocks increases; rescue/retry may only block increases; ±6 and fault cap30 remain; pt79u pair/baseline lock stays revoked'}

def apply_caps(target,configs,cutoff):
 for cfg in configs:
  for k,cap in cfg.get('soft_caps',{}).items():
   if not k.isdigit() or int(k) not in target or not isinstance(cap,dict) or cap.get('enabled',True) is False:continue
   if cap.get('until') and dt.datetime.fromisoformat(cap['until']).timestamp()<=cutoff:continue
   v=cap['cap']
   if type(v) is not int or not 0<=v<=50:raise ValueError('invalid operator cap')
   target[int(k)]=min(target[int(k)],v)
 return target

def build_tx(before,target):
 rounds=[]
 for ids in [[2,23,26,32,73],[67,68,72]]:
  old={str(c['id']):c['weight'] for c in before['channels'] if c['id'] in ids}
  rounds.append({'old':old,'dry_run':True,'applied':False,'system_target':{str(i):target[i] for i in ids}})
 w.validate(before,rounds)
 after=copy.deepcopy(before)
 for c in after['channels']:c['weight']=target[c['id']]
 for a in after['abilities']:a['weight']=target[a['channel_id']]
 for aa,bb in [(before['channels'],after['channels']),(before['abilities'],after['abilities'])]:
  for a,b in zip(aa,bb):assert {k:v for k,v in a.items() if k!='weight'}=={k:v for k,v in b.items() if k!='weight'}
 sql=w.sql(before,after);rollback=w.sql(after,before)
 for line in sql.splitlines():
  if line.startswith('UPDATE '):assert line.startswith('UPDATE channels SET weight=') or line.startswith('UPDATE abilities SET weight=')
 return after,sql,rollback

def table(before,after,ms,profiles):
 old={c['id']:c for c in before['channels']};new={c['id']:c['weight'] for c in after['channels']};lines=[]
 def val(v):return '缺测' if v is None else f'{v:.2f}'
 for name,ids in profiles.items():
  lines += ['## '+name,'|编号及实际渠道名|旧→新权重|首字p50/p90(s)与n|端到端耗时p50/p90(s)|平均端到端token/s与n|错误/总请求(率)|','|---|---|---|---|---|---|']
  for cid in ids:
   m=ms[cid];rate=100*m['error']/m['n'] if m['n'] else None
   lines.append(f"|#{cid} {old[cid]['name']}|{old[cid]['weight']}→{new[cid]}|{val(m['ttft_p50'])}/{val(m['ttft_p90'])}; n={m['ttft_n']}|{val(m['e2e_p50'])}/{val(m['e2e_p90'])}|{val(m['tps_mean'])}; n={m['tps_n']}|{m['error']}/{m['n']} ({val(rate)}%)|"+(' 小样本' if m['n']<30 else ''))
 return '\n'.join(lines)+'\n'

def verify_install():
 manifest=json.loads((ROOT/'manifest.json').read_text())
 for name,h in manifest['sha256'].items():assert digest(name)==h,'installed artifact drift: '+name
 assert manifest['main_sha']=='88e0646ff79d8b99e27164c6ae6267aa505a234b'
 return manifest

def review_schedule(ordinal):
 # Standing user schedule: after baseline48, batches49-58,59-68,69-78,79-88...
 baseline=48;interval=10
 return {'ten_round_review_due':ordinal>baseline and (ordinal-baseline)%interval==0,
         'next_review_at':baseline+((max(ordinal,baseline)-baseline)//interval+1)*interval}

def record_ledger(folder,result):
 # Same unique ledger, preserve all existing rounds and reviews, ordinal80 follows79.
 path=HOME/'learning/state.json';lock=open(path.with_name('state.lock'),'a');fcntl.flock(lock,fcntl.LOCK_EX)
 s=json.loads(path.read_text());key=str(folder)
 if key not in s['rounds']:
  ordinal=len(s['rounds'])+1
  s['rounds'][key]={'run_id':folder.name,'started':result['started'],'outcome':'verified_write' if result['applied'] else 'verified_noop','applied':result['applied'],'evidence':[str(folder/'result.json')],'ordinal':ordinal,'registered_at':now(),'improvement':{'commit':result['main_sha'],'note':'RB8 first-pass-v1 reference + independent observational review; no fabricated first-attempt evidence'}}
 atomic(path,s)
 return {'ordinal':s['rounds'][key]['ordinal'],**review_schedule(s['rounds'][key]['ordinal'])}

def main():
 os.umask(0o077);ap=argparse.ArgumentParser();ap.add_argument('--apply',action='store_true');ap.add_argument('--preflight',action='store_true');a=ap.parse_args()
 assert a.apply!=a.preflight,'choose apply or preflight'
 manifest=verify_install();policy=json.loads((ROOT/'policy.json').read_text())
 lock=open(HOME/'round.lock','a');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 assert not (HOME/'UNCERTAIN.json').exists(),'unresolved write; stop'
 if a.apply:
  checks=json.loads((ROOT/'acceptance.json').read_text());assert checks['passed'] and checks['manifest_sha256']==digest(ROOT/'manifest.json'),'tests required after any change'
 folder=HOME/'runs'/('rb8-'+dt.datetime.now(ZONE).strftime('%Y%m%d-%H%M%S%z'));folder.mkdir(mode=0o700)
 started=now();save(folder/'status.json',{'mode':'run' if a.apply else 'check-only','state':'started','started':started,'authorized_by':['pt70u']})
 cfgbytes=[p.read_bytes() for p in CONFIGS];cfghash=[hashlib.sha256(b).hexdigest() for b in cfgbytes];cfg=[json.loads(b) for b in cfgbytes]
 state_before=KRL.read_bytes();ks=json.loads(state_before);save(folder/'krl-state-before.json',ks)
 sql=export_sql();save(folder/'export.sql',sql);data=json.loads(db(sql,folder,'export'));save(folder/'export.json',data)
 before=w.canon(data['snapshot']);assert len(before['channels'])==8 and len(before['abilities'])==71
 schema=data['schema'];assert schema['custom_triggers']==0 and len(schema['weights'])==2 and all(v['type']=='int8' for v in schema['weights'])
 current={c['id']:c['weight'] for c in before['channels']}
 # Old KRL STATE last_verified_weight is frozen historical (that file is no longer written).
 # Manual drift for all 8 channels, including KRL, is caught by the controller snapshot.
 controller=ROOT/'STATE.json'
 if controller.exists():
  prior=json.loads(controller.read_text());assert prior['snapshot']==before,'manual/config/key drift: stop'
  assert prior['config_sha256']==cfghash,'operator config changed: stop for review'
 allrows=dedup(data['rows']);rows=[r for r in allrows if r['created_at']>=data['cutoff']-3600];ms=metrics(rows,data['cutoff'],policy['ids'])
 krl_eval=krl_windows(allrows,data['cutoff'],ks,json.loads(controller.read_text()) if controller.exists() else None);save(folder/'krl-window-review.json',krl_eval)
 # The live legacy schema cannot identify random first attempt, arrival maturity, protocol usability or complete route pool.
 # Do NOT label observational records random or generate fictitious SLO strata. Invoke real second algorithm with zero eligible events.
 v2in={'Cutoff':data['cutoff'],'WindowSeconds':3600,'MinUsers':3,'WeightScale':50,'EquivalenceGap':0.05,'Current':current,'Caps':{cid:50 for cid in current},'Fixed':[],'Strata':[],'Events':[]}
 save(folder/'firstpass-input.json',v2in)
 rr=subprocess.run([str(ROOT/'awfirstpass')],input=json.dumps(v2in),capture_output=True,text=True,timeout=30);save(folder/'firstpass.stderr.txt',rr.stderr);assert rr.returncode==0
 alg=json.loads(rr.stdout);save(folder/'firstpass-result.json',alg)
 target,review=independent(ms,rows,current,policy,data['cutoff']);target=apply_caps(target,cfg,data['cutoff'])
 for cid in target:
  row=alg['Channels'][str(cid)];review[cid].update(algorithm_normalized=row['target_strength'],algorithm_mapping=mapped(row['target_strength']),algorithm_status=row['Status'],final_after_caps=target[cid])
 after,applysql,rollback=build_tx(before,target)
 save(folder/'metrics.json',ms);save(folder/'assessment.json',review);save(folder/'expected-after.json',after);save(folder/'before.json',before);save(folder/'apply.sql',applysql);save(folder/'rollback.sql',rollback)
 save(folder/'algorithm-gap.json',{'observed_rows':len(data['rows']),'eligible_verified_random_first_attempts':None,'missing':['first-attempt chain','random-vs-affinity/retry provenance','available timestamp','protocol usability','complete candidate pool/order','approved fixed demand/SLO strata'],'decision':'honest abstain; independent descriptive evidence only','causal_normalized_score':None,'krl_pair_count':None,'krl_degradation_baseline':None,'krl_pair_or_baseline_blocks_rise':False,'krl_weight_policy':'same_independent_layer_as_other_gpt'})
 for p,h in zip(CONFIGS,cfghash):assert digest(p)==h,'config drift before write'
 assert KRL.read_bytes()==state_before,'concurrent KRL state change; stop'
 assert verify_install()==manifest
 if (dt.datetime.now().timestamp()-data['cutoff'])>180:raise RuntimeError('stale preimage')
 changed=sum(c['weight']!=target[c['id']] for c in before['channels']);achanged=sum(b['weight']!=target[b['channel_id']] for b in before['abilities'])
 applied=False
 if a.apply and (changed or achanged):
  save(HOME/'UNCERTAIN.json',{'run':str(folder),'state':'rb8-write-intent','authorized_by':['pt70u']})
  save(folder/'apply-output.txt',db(applysql,folder,'apply'));applied=True
 actual=w.canon(json.loads(db(READ,folder,'independent-readback')));save(folder/'readback.json',actual)
 expected=after if a.apply else before
 if actual!=expected:
  if not (HOME/'UNCERTAIN.json').exists():save(HOME/'UNCERTAIN.json',{'run':str(folder),'state':'readback-mismatch'})
  raise RuntimeError('readback mismatch; no automatic retry or rollback')
 report=table(before,actual,ms,policy['profiles']);save(folder/'tables.md',report)
 result={'state':'verified','mode':'run' if a.apply else 'check-only','applied':applied,'started':started,'finished':now(),'authorized_by':['pt70u'],'main_sha':policy['main_sha'],'manifest_sha256':digest(ROOT/'manifest.json'),'window_start':dt.datetime.fromtimestamp(data['cutoff']-3600,ZONE).isoformat(),'window_end':dt.datetime.fromtimestamp(data['cutoff'],ZONE).isoformat(),'algorithm_window':'same last1h, eligible events unavailable; all abstain','krl_source_window_count':len(ks['consumed_windows']),'krl_total_window_count':len(krl_eval['consumed_windows']),'targets':target if a.apply else current,'channel_changes':changed if a.apply else 0,'ability_changes':achanged if a.apply else 0,'only_weight_changed':True,'cache_effect_verified':False,'krl_held':target[26]==current[26],'krl_weight_policy':'same_independent_layer_as_other_gpt','run_dir':str(folder)}
 save(folder/'result.json',result)
 if a.apply:
  atomic(controller,{'snapshot':actual,'config_sha256':cfghash,'last_verified_at':result['finished'],'last_run':str(folder),'krl_source_state_sha256':hashlib.sha256(state_before).hexdigest(),'krl_recovery_policy':'special rise lock revoked 2026-11-26 pt79u; unknown-alone increase block revoked 2026-11-26 pt80u; same independent layer as other GPT; windows observational only; rescue/retry may only block increases; max step 6; fault cap30 remains','krl_consumed_windows':krl_eval['consumed_windows'],'krl_last_window_end':krl_eval['last_consumed_window_end'],'main_sha':policy['main_sha']})
  ledger=record_ledger(folder,result);save(folder/'ledger.json',ledger)
  if applied:
   assert json.loads((HOME/'UNCERTAIN.json').read_text())['run']==str(folder);(HOME/'UNCERTAIN.json').unlink()
 print(json.dumps(result,ensure_ascii=False));print(report)
if __name__=='__main__':main()
