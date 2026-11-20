# channel-autoweight

面向LLM API网关的Go自动权重计算器。

## 中文维护入口（2026-11-21）

- [算法经验本](docs/algorithm-casebook.md)：14类典型场景、证据、设计目标与当前应对差距。
- [每轮改进与四轮复盘](docs/CONTINUOUS-IMPROVEMENT.md)：确切命令、去重计数、锁和外部状态。
- [改进日志](docs/improvement-log.md)：区分算法变更、工具、测试和未修改原因。

当前研发候选ci-casebook-20261121.1不改变v5.01算法行为，未部署。生产固定二进制与独立weight-only入口不在本仓研发中替换。下文旧apply/live-probe命令仅为历史接口说明，不是当前生产运行许可：旧apply会写priority，toggle可启停；当前生产权限只允许精确范围的weight。

This is now the default (`main`) tree. The previous Python implementation is kept
on the [`python-legacy`](https://github.com/GrooveSino/channel-autoweight/tree/python-legacy)
branch and is not run or written from this branch.

Copy `config.example.json` to `config.json` and fill in your gateway host and probe settings.
Do not commit `config.json`. Runtime state lives in `data/` (`rounds.jsonl`, `shadow.jsonl`,
`direct_probe_count.jsonl`) and is gitignored.

The logic was originally aligned with Python v3.36 (write-path and toggle behavior;
defaults that were off and worse in backtests were not ported). Current settle/score
rules are described below.

## 历史CLI接口（不要据此运行当前生产）

```sh
go build -o bin/ ./cmd/...
bin/awshadow -backfill-only          # 影子补点（每 5 分钟一点，接续 shadow.jsonl 信任）
bin/autoweight -live-probe           # 只读 dry-run：探测(测试令牌)+开关+停用渠道直测+系统建议
bin/awcheck -direct ID -n 3          # 手动直测某渠道（密钥留在生产机）
AUTOWEIGHT_ALLOW_WRITE=1 bin/autoweight -apply -reuse-probe 15 \
  -expect-system 'ID=W,...' -final 'ID=W,...' -reason '...' [-apply-toggle]
                                     # 写入 channels+abilities，自动只读复核，不一致 exit 3
bin/awcheck -expect 'ID=W,...'       # 只读复核（优先级 10、abilities 同步）
```

定档（v4.98 `decide/settle.go`，配置 `settle`）：系统建议=策略目标经三条规则收口——
1h 真实请求口径（去测试令牌、客户端 4xx、跨渠道同令牌 do request failed；单令牌主导的非容量错误最多计 2 条）、
降权必须有渠道错误且样本≥30（延迟/长输出/小样本不单独降）、加权要求真实≥30 且错误率<2%、每轮 ±2、极端才清零。
报表表格与 TOKENBAN/RETRYCOST 用同一口径（features.ChanStats，记录在 `chan_60m`）。
回测：`bin/awbacktest -hours 49 -ids ... -dump f.json`，再 `-load f.json -agree 6,12,24,48 -skip 5 [-v]`
（对比人工最终权重与旧系统建议，并给出下一小时按权重的延迟/渠道错误率）。

v5.00（2026-11-18，把 11-18 反复出现的人工推翻写进规则）：
- 客户端错误不算渠道故障：`Adaptive thinking required`、`Client connection closed`、签名 400、`Unsupported parameter` 等
  （`[ReverixAI]` 新格式没有 status_code，以前被当成渠道错误）；显式404先按通用4xx排除，无标准状态码的NF标签另有集中度处理，见经验本CASE-AW-002。
- 超时不再"总计入"：单令牌集中的超时和其他单令牌错误一样去集中（429 / Concurrency limit 仍总计入）。
- `settle.cut_needs_fault`（GPT、Claude 都开）：任何降权、极端清零和系统上限（信任/新渠道/小样本/容量）都要求
  1h 真实≥30、去集中错误≥3 且≥3%；操作员 soft_caps 不受影响。
- 启停：连续倒数第一只有在有真实渠道故障时才建议停用（`toggle.rank_needs_fault`），否则只记诊断。
- 探测：`probe.hard_timeout_s` 单次硬超时、`probe.budget_s` 整轮探测预算（站点+复测+直测共用），用完即记
  `probe_budget_exceeded`，整轮不再挂住；远端脚本另有总时长截止，ssh 子进程被杀后最多再等 3 秒。

v5.01（2026-11-19，Groove 11-19 10:00「明显偏慢的渠道不该保持高权重」）：
- `pair_slow`（GPT 开，Claude 未配置=关）：同令牌配对耗时比。同令牌、同模型、同输出长度档里本渠道与其他渠道各≥3 条成功，
  比每千输出 token 耗时中位数，加权几何平均（`features/pairslow.go`）。先看 1h，配对令牌≥3 且≥30 条才用，否则看 24h。
  比≥1.6：每轮 -2、下限 10（唯一的"只因慢就降"，因为是同用户同模型同长度的证据）；1.3–1.6：不加；<1.3：常规规则回升。
  不建议停用；soft_caps 仍优先（`decide/pairslow.go`，在 Settle 之后）。24h 参考窗口每轮多取一次只读日志，TOKENPAIR 复用它。
- 错误分类适配 `[ReverixAI]` 新格式（旧格式照常）：`Model request could not be transmitted` 与 `do request failed` 同为传输失败
  （同令牌 60 秒内在别的渠道也失败=跨渠道，不算本渠道）；`Model service returned an error` 仍算上游错误（内层是客户端原因的除外）；
  "上游不支持某功能"（does not support / not supported / unsupported）与加密上下文错误不再要求 status_code=400，归客户端；
  `Invalid request parameter`、`Invalid 'input…'` 归客户端；`Model service temporarily limited` 归容量（同 429）。

边界：旧查询有只读事务；旧apply更新两表weight及priority，另可启停，并非weight-only安全入口。GPT配置priority10、Claude7。不得打印密钥；本地研发只能用脱敏fixture离线测试。

## 结构

fetch → features → trust/history → decide → toggle → apply → report，由 core 串起。
`shadow/` 影子点，`cmd/awsnap` 存快照。

## 测试

`go vet ./... && go test ./...`。单元测试每包一个文件；`regress/` 为集成测试：
`testdata/golden*` 快照回放（含 v3.32）与 0319/0401 事故回放。
