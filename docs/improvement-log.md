# 连续改进日志

## INIT-20261121（不算真实调权轮）

- 日期：2026-11-21，北京时间；依据用户01:43持续改进指令。
- 候选包：ci-casebook-20261121.1；运行算法：v5.01；运行binary SHA256 `0db48260f84ef1b93b365e023e96c4556355b595fcdc502ddfdbb5ee4c109b79`。本候选不改变算法行为、不部署。
- 候选：CASE-AW-001集中错误解释/回归缺口；独立学习账本缺少真实轮次去重与四轮复盘记录。
- 实际修改：14类中文经验本；README边界澄清；标准库本地ledger及离线单测；ChanStats→Settle的4种合成回归。核心生产Go实现、原config、runner均未改。
- 实证：20261120-231641+0800的#26 N491/Err35/Top33/Eff4/Rate.0087维持20，符合既有规则；不是为了“35错应降”而改门槛。
- 本次真实测试：Python unittest 9项通过（含12进程并发登记）；`go test ./decide ./features`通过（含新增4种跨模块回归）。`go test ./...`失败：model/TestProbeTokenIDs缺`testdata/config.json`；regress/TestReplayIncidents缺`testdata/replay_0319/config.json`与`testdata/replay_0401/config.json`。这些fixture在基准提交中就未跟踪，未用真实运行配置补洞，不宣称全包通过。其余包结果见本地测试终端记录；没有生产连接。
- 关联提交：本次首次提交（`git log -- docs/improvement-log.md`）；精确hash由提交后写入外部learning/state.json的maintenance记录，避免文档自引用hash循环。
- 当前计数：历史7轮只作种子；本次初始化=0。第一真实轮必须started≥2026-11-21 01:43北京时间，以实际run_dir登记；第4/8/12个唯一真实轮才到复盘点。
- 其他候选：request_id去重、EOF分类、远程指标契约及顺序回归均记经验本，未贸然改核心行为；不越过softcap与AZY淘汰不恢复边界。

## 每轮记录模板

run_dir唯一ID / started北京时间 / outcome（verified_write、verified_noop、failed、uncertain） / 候选CASE ID及证据 / 修改或不修改原因 / 离线测试与结果 / commit（没有则null） / 候选版本 / 实际运行版本 / 第几轮 / 待复盘cycle。

## 每四轮复盘模板

cycle及四个ordinal / 每项案例是否仍适用 / 新增或修订证据 / 理想与当前差距 / 实施、候选、否决的改进及原因 / 测试结果 / 未新增案例时明确“已复核，无新增” / 关联commit。四轮复盘不强制代码升级。

## ROUND-20261121-022156+0800（计入第 1 轮）

- 日期：2026-11-21 02:21:56–02:22:06（北京时间，Asia/Shanghai）；run_dir `20261121-022156+0800`；outcome=verified_noop；未写入生产权重。
- 运行算法：v5.01；运行 binary SHA256 `0db48260f84ef1b93b365e023e96c4556355b595fcdc502ddfdbb5ee4c109b79`。本候选不改变算法行为、不部署、不替换固定二进制。
- 候选包：ci-casebook-20261121.2。
- 候选：CASE-AW-015（本轮算法窗 CC #72：N=11、Err=2、Eff=2、Rate=18.2%，策略 0，trust 恢复期限幅 3 因 cut_needs_fault 未执行，终态维持 20）。并复核 CASE-AW-005/#32 小样本维持、CASE-AW-008/#23 策略 16→18 无故障不降。
- 实际修改：新增 CASE-AW-015 及合成回归 `TestCasebookTrustCapHeldWithoutFault`（CC 维持 20 且注记「不执行」；GPT 同形状执行 cap 到 3；达 30/3/3% 后允许系统 cap）。为 CASE-AW-005/008 补本轮脱敏证据。未改核心 settle/caps 实现、原配置、runner 或生产权限。
- 实证：仅用算法窗聚合，不把监测窗去重样本代入。不把 18.2% 或「策略 0」误报为已达降权门槛；Rate 分母为 Eff/(N-Err+Eff)=2/11。触发 cap 的 n60=15 与 ChanStat N=11 不是同一窗口。
- 测试结果与精确 commit 在提交后写入外部 learning/state.json 的本轮 annotate，避免文档自引用 hash。
- 当前计数：本轮为截点后第 1 个唯一真实调权轮；下一复盘点为第 4 轮。Mac 本轮离线，文档仅本机与仓库分支留存。
