# unknown 不得再单独禁止上调（2026-11-26，北京时间 UTC+8）

- 授权：Groove 于 2026-11-26 20:14 在 1:1 明确（pt80u）。第107/108轮 [KRL]-GPT-0.13 映射已到 36/42，却因 unknown 被禁止上调、权重卡在 30。要求立刻修复，代码该升级就升级，每次提交推送。
- 先行约束（pt79u，16:44）：30 只是事件后压到 30 的起点，随后按与其他 GPT 相同的独立层逻辑、单轮 ±6 飞回 50，不得一步跳 50。旧配对/基线恢复锁已撤。
- 本次不对生产做 weight UPDATE，不跑 `--apply`。正式入口仍是 `python3 /workspace/ops/rb8-v2-20261125/rb8.py --apply`。下一轮正式调权才按新规则写入。
- 修订前备份（含回滚文件）：`/workspace/ops/rb8-v2-20261125/unknown-unblock-20261126/backup-before-20261126-202209+0800`
- 修订前 manifest SHA256：`601dc550c3478eb5b9ef021ec7c05f590fa544e774bbeacd43e82c1d6315a2c9`
- 修订后 manifest SHA256：`d2f0c23dfade8d796ca524a703fc86db49714c5e703dd637dbb9a7da124abdd3`（已写入 acceptance.json）

## unknown 原定义与第108轮 3 条

`classify()` 将以下情况列为 unknown：缺 request_id；type=5 且 SQL `error_class` 未匹配通道故障（busy/上游5xx/408/429/超时等）或客户端（client_gone/取消/4xx）模式；type=2 零 token 且不满足显式空回复证据（或缺 frt_version=2、或 toolish）。计数来自 `metrics()` 对去重后 1h 窗内 `class=='unknown'` 的条数。`independent()` 旧规则：只要 `unknown>0` 或 `retry_not_original>0`，则 `final=min(final, old)`，对全部 GPT/CC 生效，不是 KRL 专属锁。

第108轮监测窗 2026-11-26 18:32:01–19:32:01（北京时间 UTC+8），[KRL]-GPT-0.13 独立归一 0.843、映射 42，unknown=3，客户端 2 另列，渠道故障 0、有效空回复 0、救援 0。这 3 条均为有 request_id 的 type=5，`error_class=unknown`（日志原文未导出，内容未匹配通道故障或客户端模式）：

- id 1784617，2026-11-26 19:02:26（北京时间 UTC+8），model gpt-6.2-sol，use_time=0
- id 1784629，2026-11-26 19:02:43（北京时间 UTC+8），model gpt-6.2-sol，use_time=0
- id 1785094，2026-11-26 19:12:36（北京时间 UTC+8），model gpt-6.2-sol，use_time=7

第107轮窗 17:03:27–18:03:27（北京时间 UTC+8）映射 36，unknown=2，渠道故障 0，同样被留下 30。

## 规则变更（全渠道一致，非 KRL 专属）

1. `/workspace/ops/rb8-v2-20261125/rb8.py` `independent()`：删除“unknown 单独禁止上调”。仅 `retry_not_original`（救援证据）仍可 `final=min(final, old)` 阻上调。风险帽不变：10 分钟确认渠道故障/有效空回复 ≥2，或 1 小时有效分母 ≥20 且故障 ≥2 且率 ≥5%，压到至多 30。映射仍为 `floor(50*q+.5)`，单轮 ±6。
2. 该分支对 GPT/CC 八条授权渠道同一套生效，不新增 KRL 专属锁。
3. `policy.json` 增加 `increase_blocks.unknown_alone=false`、`retry_not_original=true`；KRL 注释同步。授权追加 pt80u。
4. `test_rb8.py`、`routine-prompt-candidate.md` 已同步。

## 写后核对

- 控制器 Python 9 项回归通过（含：unknown>0 且渠道故障=0 且映射>current 时应 ±6 上调；救援仍只阻上调；风险帽仍能压到至多 30；±6 仍生效；非 KRL 渠道同样不再被 unknown 单独拦住）。
- 隔离 PostgreSQL 17 的 weight-only 0/50 apply+条件回滚通过，未连生产。
- 用第107/108轮已保存 export 离线回放：第108轮映射 42，最终 30→36（不是 30，也不是 42 或 50）；第107轮映射 36，最终 30→36。其余第108轮 7 渠目标与当轮官方结果一致。第107轮 [SWBS]-GPT-0.16 若按新规则会从官方被 unknown 拦住的 45 变为 46（该渠当时 unknown=5、故障=0、映射 46），说明修正是全渠道一致的。证据：`offline-replay-round107.json`、`offline-replay-round108.json`。
- 未跑生产 `--apply` / `--preflight`。

## 例程

候选说明已更新。本工作者没有 UpdateRoutine 工具，不能直接改已保存的 90 分钟例程正文。父 Agent 须把 `routine-prompt-candidate.md` 同步到现网例程 45-gpt-cc，否则下一轮执行器可能仍按旧提示词把 unknown 叙述成禁升理由，尽管 runner 已经走新政策。
