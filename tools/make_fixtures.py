"""Deterministic synthetic snapshots. Never reads gateway data or computes expected targets."""
import json
import sys
from test_case_manifest import manifest
from pathlib import Path

ROOT = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).resolve().parents[1] / 'testdata'
NOW = 1800000000


def save(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, sort_keys=True, separators=(',', ':')) + '\n')


def snapshot(name, ids, weights, plumbing=False):
    path = ROOT / name
    cfg = dict(windows_min=[60, 10], probe_token_ids=[9001, 9002], priority=10,
               strategy='rank', w_max=20, score=dict(a_p50=1, t_ref=20, min_n_window=5),
               strategies=dict(rank=dict(top_w=20, low_w=0)), settle=dict(enabled=not plumbing))
    if plumbing:
        cfg.update(soft_caps={'23': {'cap': 0}}, toggle=dict(enabled=True,err_rounds=1,err_probe_fail=True,hard_min=1,acct_disable={'exempt':[26]}))
    rows, events = [], []
    for ch in ids:
        for j in range(8 if plumbing else 128):
            uid=f'{ch}:{j}'
            slow=ch==ids[-1]
            ut=20 if slow else 1
            end=NOW-1-j
            rows.append(dict(t=end,ch=ch,type=2,ut=ut,ct=120,tok=j+100,m='demo',st=2))
            events.append(dict(ID=uid,User=f'u{j}',Stratum='demo/batch/short',Channel=ch,Attempt=1,Route='random',Outcome='ok',End=end,Available=end,TotalMS=ut*1000))
    save(path/'config.json',cfg)
    save(path/'snap.json',dict(version=1,now=NOW,label=name,source='synthetic',log_minutes=1440,channels=[dict(id=i,name=f'demo-{i}',weight=w,priority=10,status=1) for i,w in zip(ids,weights)],rows=rows,probes={'26':[{'ok':False}]} if plumbing else {}))
    save(path/'firstpass.json',dict(Cutoff=NOW,WindowSeconds=3600,MinUsers=3,EquivalenceGap=.02,Current=dict(zip(ids,weights)),Caps={i:(0 if plumbing and i==23 else 20) for i in ids},Strata=[dict(Name='demo/batch/short',Pool=ids,Demand=1,DeadlineMS=5000,Path='cache')],Events=events))


save(ROOT/'config.json',dict(probe_token_ids=[9001,9002]))
save(ROOT/'case-index.json',manifest())
snapshot('paired',[101,102],[20,20])
snapshot('plumbing',[2,23,26,22],[20,20,0,20],True)
