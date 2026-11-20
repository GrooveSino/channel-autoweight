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

## ROUND-20261121-030643+0800（计入第 2 轮）

- 日期：2026-11-21 03:06:43–03:06:54（北京时间，Asia/Shanghai）；run_dir `20261121-030643+0800`；outcome=verified_write；#23 渠道及 8 个 abilities 权重 18→20，其余授权渠道维持 20。
- 运行算法：v5.01；运行 binary SHA256 `0db48260f84ef1b93b365e023e96c4556355b595fcdc502ddfdbb5ee4c109b79`。本候选不改变算法行为、不部署、不替换固定二进制。
- 候选包：ci-casebook-20261121.3。
- 候选：CASE-AW-016（本轮算法窗 GPT #32 n60=7 失去首位资格，#23 被顶上后策略 20，ChanStat N=297 无错误，接受 18→20）。并复核 CASE-AW-008：恢复来自策略升权，不是 raise_clean。
- 实际修改：新增 CASE-AW-016 及合成回归 `TestCasebookRank1PromotionRecover`（三渠道形态下顶上并升权；n60=22 时次位维持 18；两渠道顶上后走全员平分维持 18；ChanStat N<30 时策略升权不加）。为 CASE-AW-008 补本轮脱敏恢复证据。未改核心 decide/strategy/settle 实现、原配置、runner 或生产权限。
- 实证：仅用算法窗聚合，不把监测窗去重样本代入。min_n_rank1=10 作用于评分窗 n60，不是 ChanStat N，也不是监测窗。两渠道平分是当前 StratRank 在“新 top 低于被取消资格者分数”时的既有行为，现网本轮因另有低于门槛的分数未触发；只锁定，不改判定。
- 测试结果与精确 commit 在提交后写入外部 learning/state.json 的本轮 annotate，避免文档自引用 hash。
- 当前计数：本轮为截点后第 2 个唯一真实调权轮；下一复盘点为第 4 轮。Mac 若离线，文档仅本机与仓库分支留存。

## ROUND-20261121-034607+0800（计入第 3 轮）

- 日期：2026-11-21 03:46:07–03:46:18（北京时间，Asia/Shanghai）；run_dir `20261121-034607+0800`；outcome=verified_noop；未写入生产权重，授权渠道全部维持 20。
- 运行算法：v5.01；运行 binary SHA256 `0db48260f84ef1b93b365e023e96c4556355b595fcdc502ddfdbb5ee4c109b79`。本候选不改变算法行为、不部署、不替换固定二进制。
- 候选包：ci-casebook-20261121.4。
- 候选：CASE-AW-017（本轮算法窗 GPT #73：评分窗 4/69=5.8% 触发 trust「1h 错误率 5.8%」，由 0.9508 降至 0.8424；ChanStat 将 4 条全部计为客户端类，N=61/Err=0/Eff=0，Settle 维持 20）。并复核 CASE-AW-001：同轮 #26 为 N=197、渠道错误 2、去集中仍为 2、Rate=1.02%，并非“总错误高但去集中后比例低”。
- 实际修改：新增 CASE-AW-017 及合成回归 `TestCasebookTrustUsesRawWindowNotChanStat`（400 客户端：trust 下降但定档维持；500 对照：trust 同样按原始窗处罚，Settle 降到 18）。为 CASE-AW-001 补本轮对照证据。未改核心 trust/settle 实现、原配置、runner 或生产权限。
- 实证：仅用算法窗聚合，不把监测窗去重样本代入。trust 读评分窗 Window，不读 ChanStat；不能把「1h 错误率 5.8%」写成渠道故障率。#26 本窗口 Eff=Err=2，集中度未压低分子。
- 测试结果与精确 commit 在提交后写入外部 learning/state.json 的本轮 annotate，避免文档自引用 hash。
- 当前计数：本轮为截点后第 3 个唯一真实调权轮；下一复盘点为第 4 轮。Mac 若离线，文档仅本机与仓库分支留存。

## ROUND-20261121-042818+0800（计入第 4 轮）

- 日期：2026-11-21 04:28:18–04:28:28（北京时间，Asia/Shanghai）；run_dir `20261121-042818+0800`；outcome=verified_noop；未写入生产权重，授权渠道全部维持 20。
- 运行算法：v5.01；运行 binary SHA256 `0db48260f84ef1b93b365e023e96c4556355b595fcdc502ddfdbb5ee4c109b79`。本候选不改变算法行为、不部署、不替换固定二进制。
- 候选包：ci-casebook-20261121.5。
- 候选：CASE-AW-018（本轮算法窗 GPT #23：ChanStat N=62、Err=2、Eff=2、Rate=3.23%，N 与 Rate 已达标但 Eff<3，维持 20；trust 按评分窗 3/68=4.4% 从 0.7858 降至 0.7292）。并复核 CASE-AW-001 的 30/3/3% 合取、CASE-AW-017 的 trust 原始窗，以及 CASE-AW-012 在 #72 全非流式时的缺测。
- 实际修改：新增 CASE-AW-018 及合成回归 `TestCasebookRateMeetsCutButEffBelowMin`（Rate 3.23% 且 Eff=2 时维持；策略已是 20 时无「达降权门槛」注记；策略 16 时写「去集中 2」；Eff=3 对照降到 18；trust 锁定 0.7292 / 4.4%）。为 CASE-AW-001/012/017 补本轮脱敏证据。未改核心 settle/trust 实现、原配置、runner 或生产权限。
- 实证：仅用算法窗聚合，不把监测窗去重样本代入。不能把监测窗 0/60、trust 4.4% 或 Rate 3.23% 单独写成已达/未达降权门槛。本轮亦完成第 1 个四轮复盘（REVIEW-CYCLE-1）。
- 测试结果与精确 commit 在提交后写入外部 learning/state.json 的本轮 annotate，避免文档自引用 hash。
- 当前计数：本轮为截点后第 4 个唯一真实调权轮，触发 cycle 1 复盘。Mac 若离线，文档仅本机与仓库分支留存。

## REVIEW-CYCLE-1（计入轮 1–4，2026-11-21）

- 周期：截点 2026-11-21 01:43（北京时间）之后第 1–4 个唯一真实调权轮；历史 7 轮只作种子，不占本周期。四个 run_dir：`20261121-022156+0800`（noop，ordinal 1）、`20261121-030643+0800`（write，#23 18→20，ordinal 2）、`20261121-034607+0800`（noop，ordinal 3）、`20261121-042818+0800`（noop，ordinal 4）。运行版本始终为 v5.01，固定二进制 sha256 `0db48260f84ef1b93b365e023e96c4556355b595fcdc502ddfdbb5ee4c109b79`，本周期没有切换运行版本或部署。
- 趋势：授权 GPT 2/23/26/32/73 与 CC 67/68/72 在第 1、3、4 轮全部维持 20；唯一生产写回是第 2 轮 #23 及 8 个 abilities 18→20（CASE-AW-016，策略顶上后的升权，不是 raise_clean）。此后 #23 连续两轮维持 20。#32、#68 持续小样本，不得排第一；第 4 轮由 #23（GPT）与 #72（CC）顶上，但目标已是 20，无新写入。#72 第 4 轮算法窗真实 67，已不再是小样本，但全部显式非流式，算法 TPS 与监测端到端速率均未取得（CASE-AW-012）。#73 流量由第 3 轮真实 61 降至第 4 轮 24，成为小样本，错误回落到 0，trust 0.8424→1。#67 第 4 轮 7 条 type5 全部为客户端类，评分窗原始错误率 7/455=1.54%<2%，trust 由 0.8581 回升至 1.0，Settle 渠道错误 0。
- 案例复核：CASE-AW-001 的 30/3/3% 合取与去集中分母仍然适用；第 3 轮 #26 为 Eff=Err=2、Rate=1.02%（集中度未再压低分子），第 4 轮由 CASE-AW-018 补上「Rate 已达 3.23% 但 Eff=2」的对照，仍维持 20。CASE-AW-005/015：小样本高比例（第 1 轮 CC #72 N=11/Eff=2/18.2%）未达门槛、trust cap 因 cut_needs_fault 不执行，后续 #72 流量回升且无渠道错误，终态保持 20，现有应对仍成立。CASE-AW-008/016：第 2 轮恢复路径已锁定，第 3–4 轮无再降再升，滞回未复现。CASE-AW-017：trust 继续只读评分窗；第 4 轮 #23 原始 4.4% 处罚信任，同时 ChanStat 仍残留 2 条渠道错误，与第 3 轮 #73「全部客户端、ChanStat 为 0」形成互补，不改公式。CASE-AW-012：第 4 轮 #72 全为 is_stream=false，缺测保持「未取得」，未编造。CASE-AW-013/014 软上限与 AZY78–80 不恢复边界本周期未被突破。其余案例无与本周期冲突的新反例。
- 代码改进效果/副作用：四轮均只提交文档与离线回归（候选包 ci-casebook-20261121.2–.5），没有算法行为变更，也没有替换运行二进制。副作用是全包 `go test ./...` 仍因基准提交缺少 testdata 配置 fixture 失败，未用真实配置补洞；相关单测 `go test ./decide ./features ./trust` 与 `python3 -m unittest discover tools` 通过。未发现回归测试改变现网建议。
- 待解决问题与测试完善：trust 与 Settle 口径是否对齐（CASE-AW-017）仍是候选，未做反事实回放；Rate≥3% 但 Eff<3 是否需要单独观察策略（CASE-AW-018）同为候选；非流式渠道的 TPS/端到端速率缺测（CASE-AW-012）仍未接通远程指标；#32/#68 长期薄样本与会话亲和导致改权见效慢，只记录不改规则。不在本复盘改 30/3/3%、raise_clean、min_n_rank1 或生产权限。
- 结论：已逐项复核第 1 至 4 个唯一轮。本周期新增并锁定 CASE-AW-015/016/017/018，均为现有 v5.01 行为的回归与解释，不作为算法升级。无需要立即变更的生产规则。下一复盘点为第 8 个唯一真实轮。

## ROUND-20261121-052017+0800（计入第 5 轮）

- 日期：2026-11-21 05:20:17–05:20:28（北京时间，Asia/Shanghai）；run_dir `20261121-052017+0800`；outcome=verified_noop；未写入生产权重，授权渠道全部维持 20。
- 运行算法：v5.01；运行 binary SHA256 `0db48260f84ef1b93b365e023e96c4556355b595fcdc502ddfdbb5ee4c109b79`。本候选不改变算法行为、不部署、不替换固定二进制。
- 候选包：ci-casebook-20261121.6。
- 候选：CASE-AW-019（本轮算法窗 GPT #73：10m 归一化 np50 70.5s > 2.5×同伴中位 25.4s，分数×0.90；ChanStat N=24、Err=1、Eff=1、Rate=4.17%，未达 30/3/3%，策略 0 被定档回 20）。并复核 CASE-AW-017（trust 仍读评分窗 1/28=3.57%，注记 3.6%，1.0→0.9526；#23 无原始错误，0.7292→0.9292 回升）与 CASE-AW-005（#32/#68/#72 本窗 1h 真实 0，#2 真实 17，均维持 20）。
- 实际修改：新增 CASE-AW-019 及合成回归 `TestCasebookSlowBurstPenaltyDoesNotCut`（活样本因子与注记；等于阈值/样本不足/关闭时不罚；Decide+Settle 维持 20；达故障门对照降到 18）。为 CASE-AW-017/005 补本轮脱敏证据。未改核心 score/settle 实现、原配置、runner 或生产权限。
- 实证：仅用算法窗聚合，不把监测窗去重样本代入。不能把分数×0.90 或 trust 3.6% 写成已经降权。监测窗将 #73 去重后 1 条 type5 记为客户端类，与算法窗 Client=0 窗口不同，不作分类反例。本轮不是四轮复盘点（下一复盘为第 8 轮）。
- 测试结果与精确 commit 在提交后写入外部 learning/state.json 的本轮 annotate，避免文档自引用 hash。
- 当前计数：本轮为截点后第 5 个唯一真实调权轮；下一复盘点为第 8 轮。Mac 本轮离线，文档仅本机与仓库分支留存。
