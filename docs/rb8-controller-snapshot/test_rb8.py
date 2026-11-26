import copy,json,math,unittest
from pathlib import Path
import rb8
class RB8Tests(unittest.TestCase):
 def setUp(self):self.p=json.loads((rb8.ROOT/'policy.json').read_text())
 def test_original_48_based_review_boundaries(self):
  for n,due,nxt in [(48,False,58),(49,False,58),(58,True,68),(68,True,78),(78,True,88),(79,False,88),(80,False,88),(87,False,88),(88,True,98),(98,True,108)]:
   self.assertEqual(rb8.review_schedule(n),{'ten_round_review_due':due,'next_review_at':nxt})
 def test_normalization(self):
  for v in [float('nan'),float('inf'),-float('inf'),None]:
   with self.assertRaises(ValueError):rb8.normalized(v)
  self.assertEqual(rb8.mapped(0),0);self.assertEqual(rb8.mapped(1),50);self.assertEqual(rb8.mapped(.347),17);self.assertIsNone(rb8.mapped(None))
  self.assertLess(rb8.normalized(.4),rb8.normalized(.9))
 def row(self,cid,i,lat):
  return {'id':cid*10000+i,'created_at':10000-i,'type':2,'channel_id':cid,'rid':str(cid)+'-'+str(i),'has_rid':True,'tok':str(i%5),'ct':500,'pt':100,'model_name':'same','is_stream':True,'use_time':lat,'ttft':lat/4,'class':'success','error_class':None}
 def test_disparity_and_missing(self):
  rows=[self.row(c,i,lat) for c,lat in [(2,8),(23,32),(26,8),(32,16),(73,8),(67,8),(68,16),(72,24)] for i in range(80)]
  current={cid:40 for cid in self.p['ids']};current[26]=30
  ms=rb8.metrics(rows,10001,self.p['ids']);target,r=rb8.independent(ms,rows,current,self.p,10001)
  self.assertGreater(target[2],target[23])
  # KRL uses the same independent layer; good evidence may raise 30, but never skip the ±6 cap to 50.
  self.assertGreater(target[26],30);self.assertLessEqual(target[26],36);self.assertNotEqual(target[26],50)
  self.assertGreater(r[2]['independent_normalized_reference'],r[23]['independent_normalized_reference'])
  self.assertFalse(any('keep30' in x or 'strict recovery' in x for x in r[26]['reasons']))
  empty=rb8.metrics([],10001,self.p['ids']);t,r=rb8.independent(empty,[],current,self.p,10001);self.assertEqual(t,current);self.assertIsNone(r[2]['independent_normalized_reference'])
 def test_fault_recovery_and_caps(self):
  current={cid:44 for cid in self.p['ids']};current[26]=32
  rows=[]
  for i in range(3):
   r=self.row(26,i,10);r.update(type=5,ct=0,error_class='channel',**{'class':'channel_fault'});rows.append(r)
  m=rb8.metrics(rows,10001,self.p['ids']);t,_=rb8.independent(m,rows,current,self.p,10001);self.assertEqual(t[26],30)
  t=rb8.apply_caps(t,[{'soft_caps':{'26':{'cap':20,'enabled':True}}}],10001);self.assertEqual(t[26],20)
 def test_no_rescue_no_fake_empty(self):
  r=self.row(2,1,10);r['ct']=0;r['pt']=0
  self.assertEqual(rb8.classify(r,1),'unknown')
  r['ct']=50;self.assertEqual(rb8.classify(r,2),'retry_not_original')
  r.update(ct=0,pt=100,frt_v2=True,timing_measured=True,chunks=0,end_reason='eof',toolish=False);self.assertEqual(rb8.classify(r,1),'explicit_empty')
  r['frt_v2']=False;self.assertEqual(rb8.classify(r,1),'unknown')
  r['frt_v2']=True;r['toolish']=True;self.assertEqual(rb8.classify(r,1),'unknown')
 def test_krl_nonoverlap(self):
  source={'consumed_windows':[],'last_consumed_window_end':'1970-01-01T08:00:00+08:00'}
  v=rb8.krl_windows([],3600,source);self.assertEqual(len(v['consumed_windows']),2)
  # Window ledger remains; pair/baseline stay null as observation and no longer lock weight.
  self.assertTrue(v['rise_allowed']);self.assertFalse(v['special_rise_lock'])
  self.assertEqual(v['weight_policy'],'same_independent_layer_as_other_gpt')
  prior={'krl_consumed_windows':v['consumed_windows'],'krl_last_window_end':v['last_consumed_window_end']}
  v2=rb8.krl_windows([],5400,source,prior);self.assertEqual(len(v2['consumed_windows']),3);self.assertIsNone(v2['consumed_windows'][-1]['pairs']);self.assertIsNone(v2['consumed_windows'][-1]['degradation_baseline'])
 def test_unknown_alone_does_not_block_increase_and_step_cap(self):
  rows=[self.row(c,i,lat) for c,lat in [(2,8),(23,32),(26,8),(32,16),(73,8),(67,8),(68,16),(72,24)] for i in range(80)]
  current={cid:40 for cid in self.p['ids']};current[26]=30
  # Locked 108-round scenario: unknown>0, channel_fault=0, mapping>current -> ±6 up, not hold 30, not jump to mapping/50.
  unknown=self.row(26,999,8);unknown.update(type=5,ct=0,error_class='unknown',**{'class':'unknown'});rows_u=rows+[unknown]
  ms=rb8.metrics(rows_u,10001,self.p['ids']);t,r=rb8.independent(ms,rows_u,current,self.p,10001)
  self.assertGreater(ms[26]['classes']['unknown'],0)
  self.assertEqual(ms[26]['classes']['channel_fault'],0)
  self.assertIsNotNone(r[26]['independent_mapping'])
  self.assertGreater(r[26]['independent_mapping'],30)
  self.assertEqual(t[26],min(30+self.p['max_performance_step'],r[26]['independent_mapping']))
  self.assertGreater(t[26],30);self.assertLessEqual(t[26],36);self.assertNotEqual(t[26],42);self.assertNotEqual(t[26],50)
  self.assertFalse(any('unknown' in x and 'blocks increases' in x for x in r[26]['reasons']))
  # Same rule on a non-KRL GPT channel: unknown alone does not hold the old weight.
  unknown2=self.row(2,999,8);unknown2.update(type=5,ct=0,error_class='unknown',**{'class':'unknown'})
  current2=dict(current);current2[2]=30
  ms2=rb8.metrics(rows+[unknown2],10001,self.p['ids']);t2,r2=rb8.independent(ms2,rows+[unknown2],current2,self.p,10001)
  self.assertGreater(ms2[2]['classes']['unknown'],0);self.assertEqual(ms2[2]['classes']['channel_fault'],0)
  self.assertGreater(t2[2],30);self.assertLessEqual(t2[2],36)
  # Rescue/retry evidence may still only block an increase, not force a decrease.
  retry=self.row(26,998,8);retry.update(**{'class':'retry_not_original'})
  ms_r=rb8.metrics(rows+[retry],10001,self.p['ids']);t_r,r_r=rb8.independent(ms_r,rows+[retry],current,self.p,10001)
  self.assertEqual(t_r[26],30)
  self.assertTrue(any('rescue/retry evidence blocks increases' in x for x in r_r[26]['reasons']))
  # From 44, a strong mapping still cannot jump to 50 in one round.
  current[26]=44
  ms=rb8.metrics(rows,10001,self.p['ids']);t3,r3=rb8.independent(ms,rows,current,self.p,10001)
  self.assertLessEqual(t3[26],50);self.assertLessEqual(t3[26]-44,6)
  if r3[26]['independent_mapping'] is not None and r3[26]['independent_mapping']>=50:
   self.assertEqual(t3[26],50)
 def test_fault_cap30_still_applies_with_unknown(self):
  # Risk cap must still press to at most 30 when confirmed channel faults meet the threshold.
  current={cid:44 for cid in self.p['ids']};current[26]=32
  rows=[self.row(c,i,lat) for c,lat in [(2,8),(23,32),(26,8),(32,16),(73,8),(67,8),(68,16),(72,24)] for i in range(80)]
  faults=[]
  for i in range(3):
   r=self.row(26,80+i,10);r.update(type=5,ct=0,error_class='channel',created_at=10001-10-i,**{'class':'channel_fault'});faults.append(r)
  unknown=self.row(26,999,8);unknown.update(type=5,ct=0,error_class='unknown',**{'class':'unknown'})
  rows_f=rows+faults+[unknown]
  ms=rb8.metrics(rows_f,10001,self.p['ids']);t,r=rb8.independent(ms,rows_f,current,self.p,10001)
  self.assertGreaterEqual(ms[26]['recent_faults'],2)
  self.assertEqual(t[26],30)
  self.assertTrue(any('channel-fault protection cap30' in x for x in r[26]['reasons']))
  # 1h path: fault_den>=20, faults>=2, rate>=5% also caps at 30 even if recent window is clean.
  rows_old=list(rows)
  for i in range(5):
   r=self.row(26,800+i,10);r.update(type=5,ct=0,error_class='channel',created_at=10001-700,**{'class':'channel_fault'});rows_old.append(r)
  # 80 successes + 5 hour-old faults: den=85, rate=5/85>=5%, recent_faults=0
  ms_h=rb8.metrics(rows_old,10001,self.p['ids']);t_h,r_h=rb8.independent(ms_h,rows_old,current,self.p,10001)
  self.assertEqual(ms_h[26]['recent_faults'],0)
  self.assertGreaterEqual(ms_h[26]['fault_den'],20)
  self.assertGreaterEqual(ms_h[26]['faults'],2)
  self.assertGreaterEqual(ms_h[26]['fault_rate'],0.05)
  self.assertEqual(t_h[26],30)
  self.assertTrue(any('channel-fault protection cap30' in x for x in r_h[26]['reasons']))
 def test_exact_scope_and_six_columns(self):
  b=json.loads(rb8.w.BASE.read_text());b['database_identity']={'database':'newapi','system_identifier':rb8.w.EXPECTED_SYSTEM_IDENTIFIER}
  for c in b['channels']:c['models']='fixture'
  b=rb8.w.canon(b);target={c['id']:50 for c in b['channels']};target[26]=30
  after,sql,rollback=rb8.build_tx(b,target)
  self.assertNotIn('SET priority',sql);self.assertNotIn('SET status',sql);self.assertEqual(len(after['abilities']),71)
  for name in ['nan','extra','missing','disabled']:
   bad=copy.deepcopy(b);t=dict(target)
   if name=='nan':t[2]=math.nan
   if name=='extra':bad['abilities'].append(dict(bad['abilities'][0],model='EXTRA'))
   if name=='missing':bad['abilities'].pop()
   if name=='disabled':bad['channels'][0]['status']=2
   with self.assertRaises(ValueError):rb8.build_tx(bad,t)
  txt=rb8.table(b,after,rb8.metrics([],1,self.p['ids']),self.p['profiles'])
  for line in txt.splitlines():
   if line.startswith('|'):self.assertEqual(line.count('|'),7)
if __name__=='__main__':unittest.main()
