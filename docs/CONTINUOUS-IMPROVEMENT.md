# 每轮改进与四轮经验复盘：可执行工作约定

建立时间2026-11-21 01:43指令之后，北京时间。候选包：ci-casebook-20261121.1。它是工具/测试/文档，不是新算法版本，不替换当前v5.01。

## 1. 权限与两把不同的锁

- 本机checkout `/workspace/repos/channel-autoweight`；专用持续改进分支 `improve/algorithm-casebook-20261121`。用已认证gh/git；保留dirty，不reset、不force，不合main、不发Release、不部署。
- 研发改repo前独占 `/workspace/ops/channel-weight-only/learning/improvement.lock`，持有到提交/push完成。可以用 `flock -n /workspace/ops/channel-weight-only/learning/improvement.lock bash` 开一个持锁shell，在该shell内完成工作；若返回忙，不能同时改仓库。文件存在不等于锁仍持有，禁止删除别人的锁文件。
- 工具自行用同目录 `state.lock` 跨进程flock读改写state。它与研发锁分开：持研发锁时可以调用工具，不会自锁；多个通知可安全登记。
- 绝不改生产使用的原运行树、原JSON配置、原固定binary、`run_round.py`或`weight_sql.py`；不运行生产调权/探测，不连接生产。日志登记是事后维护，不是新的runner或scheduler。不得以本流程改变softcap、淘汰范围或启停。

## 2. 状态稳定存储，不随分支消失

权威状态：`/workspace/ops/channel-weight-only/learning/state.json`，不是Git跟踪文件。目录0700、文件0600；每次更新持state.lock，写同目录临时文件、fsync、os.replace、目录fsync。故障不截断旧state；若replace后目录fsync失败，应先status回读而不是认定绝未更新。损坏/未知schema拒绝覆盖，需显式迁移；不能删除state重置周期。

工具源码：`tools/learning_state.py`；已安装稳定副本：`/workspace/ops/channel-weight-only/learning/learning_state.py`。下面命令全部用稳定副本，因此切到没有tools的新分支也不丢计数；改工具须离线测试通过、在研发锁下显式更新安装副本。安装版及文档的sha256记录在learning/install-manifest.json，禁止提交整个learning目录或runs。

工具仅Python标准库，**不执行runner、不读原配置/原日志、不调用git/网络/数据库**；只读取选定run_dir的status.json/result.json，再抽取模式、时间、结果。自由文本只填短小脱敏总结，不贴原始日志或密钥；工具不是完整DLP检测器。

## 3. 计数的精确定义

- 截点：`2026-11-21T01:43:00+08:00`（北京时间）。以真实轮次status/result里的started为准，不以通知或脚本调用时间为准；跨截点完成但启动在此前的仍是历史种子。
- 初始化读现有7个mode=run终态轮作历史种子，count=0：20261120-210743、215230、222750、231641，20261121-000110、004258、013152（均+0800）。20:53 check-only不计。init不自动吸纳截点后的轮次，也不能把本次代码检查当第1轮。
- 截点后的首个真实mode=run轮记1。当前调度交接下一轮预计2026-11-21 02:15:58北京时间，可晚5分钟；这是预期，不先建虚构run，也不保证它必为第1轮（若有更早实际run，以证据为准）。
- 唯一身份是真实run_dir的canonical absolute path；符号链接/重复通知不多计，禁止将同一轮复制改名后当新轮登记。序号按首次登记分配且不回排；若补登旧轮，要说明补登但不能重编号已完成复盘。
- 成功写回verified_write、成功无变化verified_noop、failed、uncertain均占一轮；失败/未知的applied=null，不冒称已写或未写。check-only、手动检查、初始化、代码测试不计。未结束轮拒绝登记；缺result的真实失败须操作者确认runner已结束后明确标记。
- 第4个唯一轮登记后review_due=[1]，第8个后cycle2，以此类推。不因重复通知变成第5轮；失败不逃出周期。每4轮必须复盘，但绝不等于每4轮必须改算法。

## 4. 直接可用的命令与输出

以下均本地维护，**不要把生产runner命令拼进这些步骤**。

### 初始化/查看（可重复）

```sh
python3 /workspace/ops/channel-weight-only/learning/learning_state.py init
python3 /workspace/ops/channel-weight-only/learning/learning_state.py status
```

输出JSON含counted_rounds、historical_seed_count、next_ordinal、next_review_at、review_due、初始化运行版本。init合并已有seed和state，不覆盖已登记轮次。status只回读，不计轮。

### 每个真实轮结束后登记一次（RUN_DIR必须换成实际存在的路径）

```sh
RUN_DIR=/workspace/ops/channel-weight-only/runs/实际本轮目录名
python3 /workspace/ops/channel-weight-only/learning/learning_state.py record \
  --run-dir "$RUN_DIR" \
  --candidate CASE-AW-001 \
  --change-kind none \
  --candidate-version no-new-version \
  --tests '未执行新测试；仅复核本轮聚合证据' \
  --note '证据不足以安全改变算法，本轮维持；已复核典型场景'
```

record只接受runs根目录下直接子目录，mode必须run，started必须带时区且不早于截点。结果JSON含duplicate、round.ordinal、round.outcome和review_due。重复record原样返回既有条目，不覆盖改进资料。若未取得commit，不填--commit，保存null；不能把pending伪装hash。

失败缺result的显式登记：上述命令加 `--outcome failed`；提交状态不明则加 `--outcome uncertain`。若result已verified则拒绝改标失败；不能用该选项把仍运行的程序当失败绕过等待。

### 改进完成后更新该轮的改进资料（不增加计数）

```sh
python3 /workspace/ops/channel-weight-only/learning/learning_state.py annotate \
  --run-dir "$RUN_DIR" --candidate CASE-AW-001 --change-kind tests \
  --tests 'go test ./decide ./features PASS；无生产连接' \
  --commit 实际提交hash --candidate-version 实际候选包标签 \
  --note '仅新增脱敏回归，不改变决策行为；运行仍v5.01'
```

change-kind可为none/docs/tests/tooling/algorithm。字段同时记录code_modified、algorithm_behavior_changed；tests/tooling不等于算法改变。annotate保留improvement_history，不改真实轮的序号、started、结果。算法行为若需改动先列为候选设计，不能靠填algorithm直接生效。当前--runtime-version只允许v5.01，binary hash固定；未来真实部署升级须另行授权并显式迁移版本记录，不能静默改标签。

### 每4轮复盘

先status查看review_due；例如due=[1]才执行：

```sh
python3 /workspace/ops/channel-weight-only/learning/learning_state.py review \
  --cycle 1 --case-id CASE-AW-001 \
  --conclusion '已逐项复核第1至4个唯一轮：错误口径、低样本、PairSlow、人工变更与淘汰边界；无新增案例，现有应对及待证项保持，不改算法'
```

这只是格式示例，必须按四轮真实证据填写，不能直接伪造已复核。也可不带case-id、带真实--commit。少于4轮拒绝；必须非空结论；重复同cycle不重复登记、不覆盖既有复盘。review_due保持未完成周期，直到真的review；即使已到第5/9轮也要补做旧周期。把复盘摘要及案例变化另记repo经验本/改进日志，再提交；state不会自行改仓库文档。

### 初始化或其他非调权维护记录（绝不计轮）

```sh
python3 /workspace/ops/channel-weight-only/learning/learning_state.py maintenance \
  --id bootstrap-20261121 --candidate CASE-AW-001 --change-kind tooling \
  --candidate-version ci-casebook-20261121.1 --commit 实际提交hash \
  --tests '账本离线单测与Go回归通过' \
  --note '初始化经验本、工具和集中错误回归；不是调权轮'
```

maintenance按id去重。测试隔离路径可用全局参数放在子命令之前：`python3 tools/learning_state.py --state-file /tmp/example-learning/state.json --runs-root /tmp/example-runs status`。不向真实state注入合成轮次。

## 5. 每轮研发工作流

1. 登记实际轮，回读state；区分成功写、no-op、失败、未知，不把收益/指标编出来。
2. 独占improvement.lock；核对remote/status/branch并保留所有dirty。查经验本相关case及本轮脱敏证据。
3. 尽量找一个真实可复现缺口；核心算法行为先作候选设计，比较历史影响，不擅改大口径。无安全改进空间明确记原因。
4. 只用合成fixture/隔离环境离线测试。当前可运行：
   `python3 -m unittest discover -s tools -p 'test_*.py' -v`
   `go test ./decide ./features`
   `go test ./...`
   测试不需要PG/SSH/生产配置。本次全包测试因基准提交缺少三个testdata配置fixture而失败，不能把局部回归通过写成全包通过；不得拿真实运行config补洞。不得执行legacy apply/live-probe/scheduler。
5. 更新中文casebook、docs/improvement-log.md；检查diff无秘密和无关变动，本地commit，普通push专用非main分支；不创建PR/Release、不部署。
6. annotate回填实际hash/测试/候选版本；若state提示四轮周期到期，逐项复盘并review；无新案例也要写结果。释放研发锁。

## 6. 本地文件与Mac

仓库中的README.md及docs是实际中文文件。离线归档副本在learning/docs；可由父Bot使用已连接Mac复制至 `~/grokbot/主站权重/算法经验本/`。本任务未触碰用户Mac，不能把“本机已落盘”说成“Mac已同步”。状态和包含本机证据路径的外部文件不提交GitHub；只复制用户需要的文档与工具，不复制原配置/日志。
