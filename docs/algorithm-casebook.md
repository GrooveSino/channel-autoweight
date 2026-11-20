# 自动权重机算法经验本

版本：ci-casebook-20261121.1（文档、工具与回归测试候选，不是算法升级或部署）。建立于2026-11-21，北京时间。维护办法见[连续改进约定](CONTINUOUS-IMPROVEMENT.md)。

**边界**：每项“理想应对”都是待验证设计目标，不等于用户已授予新调权规则。只在专用分支离线研究；生产仍用固定v5.01二进制及独立weight-only入口。不能为了凑版本改错误口径、决策顺序、软上限、停用规则。证据只有脱敏聚合/源码；不收集密钥、真实token值、原配置、请求正文或原始日志。

## 证据索引

- S1：原算法源码基准999993b6b8d654df6e6445ad5b8d3df52ae17640；与原本机e0cebd897a46d424554b037a6305c32b5938c679的131个Go文件及go.mod逐字节相同；不是二进制可复现构建证明。
- S2：本机`/workspace/reports/autoweight-explainer-20261120/source-review/source-review-zh.md`及同目录配置对照。2026-11-20静态审查；这是本机资料，不把全文拷进GitHub。
- S3：本机`/workspace/ops/channel-weight-only/runs/<run_id>/gpt-calculation.json`、`result.json`。只摘下述聚合值；所有日期时间为北京时间。
- S4：本机`/workspace/ops/channel-weight-only/README.md:3–9`与本轮委托提供的生产入口说明：8渠道、61个ability复合键、只weight、备份/条件回滚/锁/回读。属于已有交接证据，本线程没有连接生产或重跑PG验收。
- S5：2026-11-21 01:43用户指令：每轮尽量改善；每4轮复盘；没有安全改善空间可以不改。AZY78–80质量淘汰不恢复来自本轮任务边界；未另查这些标签当前生产ID映射/状态。

## CASE-AW-001：高总错误不等于渠道故障门已达标

- 场景/日期/证据：2026-11-20 23:16:41轮`20261120-231641+0800`，#26 ChanStat N=491、Err=35、TopErr=33、Eff=4、Rate=.0087，old=system=20（S3）。2026-11-21 01:31:52轮另为N394/Err6/Eff6/.0152，不能把两个窗口拼接。
- 理想应对（设计目标）：准确解释去集中与容量错误；既不被单一客户故障拖走，又不掩盖真实容量不足；边界变化应通过反事实样本。
- 当前真实处理/依据：`features/chanerr.go:104–165`，其他token仍成功且最大token占非容量错误≥80%，其错误最多计2；Eff=35−33+2=4。**4/491也低于3%，但代码真正Rate=4/(491−35+4)=4/460=.0087**。`decide/settle.go:59–80`要求N≥30、Eff≥3、Rate≥.03才普通降；因此维持20与原规则完全不矛盾。429/并发容量不折扣。
- 差距：解释层易拿35/491直接认定应降，或把4/491误称代码Rate；需固定跨模块回归，不应贸然改算法。
- 测试/验收：`decide/casebook_test.go:TestCasebookConcentrationThroughSettle`，合成491条，覆盖集中35→4维持、分散35降2、429不折扣降2、400排客户端维持；只保留聚合形状，使用虚构token编号。
- 状态/关联：本次新增并离线执行；属于现有行为回归锁定，不改变阈值。关联本分支首次提交（`git log -- decide/casebook_test.go`）。

## CASE-AW-002：客户端错误与真故障分离

- 场景/证据：S1/S2静态审查2026-11-20；CASE001本次合成400对照。不是新生产故障样本。
- 理想应对：客户端签名、无效参数等不惩罚渠道；容量和真实上游失败仍计入；避免跨渠道共现等于同一请求的误推断。
- 当前/依据：`features/window.go:53–70`、`features/chanerr.go:20–75`；4xx除408/429先排；同token±60秒另一渠道错误可排Df；评分窗cross_channel与ChanStat并非同口径。
- 差距：显式404被通用4xx先排；源码注释称“分散404仍算”不能概括全部。跨渠道匹配没有request_id证明。
- 验收：本次400/429对照；后续补有/无status、内容标签、跨渠道时间边界和不同request_id样本。
- 状态/关联：部分回归覆盖，分类重构仅候选；本次CASE001测试提交。

## CASE-AW-003：request_id重复日志

- 场景/证据：2026-11-20 S1/S2静态；主SQL`fetch/psql.go:143–179`未选request_id，Row无该字段。没有据此杜撰已发生重复事故。
- 理想应对：先定义可验证唯一键及跨type的取舍，再稳定去重，保留缺键行，报告raw/dedup数量。
- 当前/依据：主算法按行计；外部monitor有独立n_raw/n_dedup，但不代表Go评分已去重；`toggle/streaks.go`的轮记录去重不是日志请求去重。
- 差距：重复会改变评分N/错误分母，不能无设计直接删除行或把空request_id合并。
- 验收：未来合成同ID重复、跨渠道重试、缺ID、不同类型冲突；离线对比历史聚合影响才讨论候选开关。
- 状态/关联：known-gap，未改计数；首次经验本文档提交。

## CASE-AW-004：type2零输出与EOF

- 场景/证据：2026-11-20 S1/S2；`fetch/psql.go:154–175`未取EOF原因；不是宣称本轮出现EOF。
- 理想应对：可归因的上游异常空结束与客户端主动取消/空请求分开；不能把所有零输出都算失败。
- 当前/依据：`features/window.go:115–150` type2仍成功，可计empty；Ue空请求免empty；`features/chanerr.go:124–134`非type5仍成功。主链不会自动把type2 EOF转失败。
- 差距：历史人工约定与自动分类不完全一致。
- 验收：先补EOF原因抽取与unknown表示的设计；合成EOF、取消、无输出但合法响应、无结束原因四类；不直接改现有生产分母。
- 状态/关联：design-proposal，待证据；首次经验本文档提交。

## CASE-AW-005：低样本、零流量、防饿死

- 场景/证据：S2交接2026-11-20 19:22，#32真实10、CC72真实4、CC67零样本；不是当前状态查询。
- 理想应对：证据不足避免过度升降；0流量不等于健康；授权范围内恢复探索应与人工禁用区分。
- 当前/依据：`decide/settle.go:72–82`普通升权需30，但old<floor且干净可先到2；`decide/decide.go:264–280`新渠道探索可后置；无数据rank也非自动均分，`decide/strategy.go:73–108`将零首分替极小正数。
- 差距：“<30绝不动”不含这些例外；样本N分评分窗/mix/ChanStat三类。
- 验收：保留现有`decide/v500_test.go`，未来补全零样本、0→2、已淘汰禁复活的外层拒绝测试。
- 状态/关联：documented-current，未新增恢复授权。
- 补充证据：2026-11-21 02:21:56 轮 `20261121-022156+0800` GPT #32 评分窗 n60=22，权重维持 20；CC #72 算法窗 ChanStat N=11 未达普通降权样本门，详见 CASE-AW-015。监测窗是另一口径，不能直接代入。S3。

## CASE-AW-006：PairSlow后置覆盖Settle

- 场景/证据：S1/S2静态2026-11-20；GPT开、Claude未配；不是新慢渠道事故。
- 理想应对：纯延迟规则作为明确可解释例外；不能误称任何降权都必须故障；不得越过softcap/淘汰范围。
- 当前/依据：`decide/decide.go:262–263`先Settle后PairSlow；`decide/pairslow.go:13–65`，ratio≥1.6按old−2至10，1.3–1.6禁升；1h不够转24h参考，当前1h<30仍可用历史证据降。
- 差距：通用“30/3/3%”描述不覆盖这条；shadow路径缺正式链AttachPair，不可等同正式建议。
- 验收：现有`decide/v501_test.go:TestPairSlow`；下一步候选是增加跨Settle/softcap顺序端到端回归，而不是改顺序。
- 状态/关联：documented-current，本次不变更。

## CASE-AW-007：慢与故障的取舍

- 场景/证据：S1/S2；Settle strict=true两组均有，PairSlow仅GPT。
- 理想应对：可靠性与延迟分别解释；长任务、排队、推理耗时不能简单作故障；极端故障可优先止损。
- 当前/依据：`decide/score.go:127–153`耗时参与分；`decide/settle.go:58–82`无bad通常维持，extreme可归0但strict仍需bad；GPT PairSlow另例外。
- 差距：低分不等于直接减权；纯延迟指标不能证明上游错误。
- 验收：未来同错误率不同延迟、长输出对照及极端故障合成集，明确每级目标。
- 状态/关联：design-proposal，未调整权衡系数。

## CASE-AW-008：升权踏步、滞回、冷却

- 场景/证据：S2原配置raise_clean=false、rank hysteresis/band/ramp=0；配置中rise_confirm不等于Go已接线。
- 理想应对：避免往复而不无故拖延真实恢复；“干净必回爬”若需启用必须先验证。
- 当前/依据：`decide/settle.go:70–82`只有策略提出升才走+2，raise_clean关闭时不强行回20；trust慢升快降影响分；`decide/strategy.go:286–307`HoldTop开关当前0。
- 差距：人工回爬约定不是无条件自动实现；没有证据证明当前需要启用新冷却参数。
- 验收：合成分数交替/恢复/无样本序列，比较翻转数和恢复耗时，先影子候选不部署。
- 状态/关联：design-proposal。
- 补充证据：2026-11-21 02:21:56 轮 `20261121-022156+0800` GPT #23，算法窗真实 392、渠道错误 0、去集中 0，策略 16→18（无渠道级错误证据，延迟/小样本不单独降权），终态维持 18；符合 raise_clean 关闭时不因策略下调而无故障降权。S3。
- 补充证据：2026-11-21 03:06:43 轮 `20261121-030643+0800` GPT #23 由 18 升至 20。这不是 raise_clean 回爬（该开关仍关），而是 #32 失去首位资格后 #23 被顶上、策略改为 20，settle 在 N=297 且无错误时接受 +2。对照见 CASE-AW-016。S3。

## CASE-AW-009：模型、输出长度与用户混杂

- 场景/证据：S1/S2，workload_match开启，len_norm关闭但仍计算指标；PairSlow按token/model/长度格。
- 理想应对：同模型同负载比较，说明剩余混杂，不能把多模型全量p50当因果证明。
- 当前/依据：`features/window.go:211–262`、`decide/score.go:139–153`；workload_match按长度不按model/token，pool是channel分位的样本加权均值；`features/pairslow.go:18–75`为更严格配对。
- 差距：无公共cell时不能证明谁快；TPS端到端ct/(ut+.5)含等待/推理。
- 验收：合成模型配比、长短输出、用户偏斜的反转例，固定样本门及unknown输出。
- 状态/关联：documented-current，未改评分。

## CASE-AW-010：外部手改与并发漂移

- 场景/证据：S2交接#32/#73曾手动降低；不把旧20擅自恢复。S4外部writer已有前像/锁约束。
- 理想应对：以实际前像决定权限；并发变更冲突退出，不静默覆盖；“建议漂移”和“DB前像漂移”分开。
- 当前/依据：old从DB读，`diag/extwrite.go:10–43`只是诊断；`core/manual.go:15–35`expect-system不是CAS。旧apply无CAS；独立weight-only入口据S4对完整前像验证。
- 差距：EXTWRITE不构成长期人工锁；本次没有独立复验生产并发。
- 验收：隔离测试写前漂移、同值碰撞、未知提交禁止盲重试；不改live runner来完成本次研发。
- 状态/关联：guard-external，生产验收不在本提交范围。

## CASE-AW-011：61键精准写回与回滚

- 场景/证据：2026-11-21任务交接及S4：channels PK=id，abilities PK=(group,model,channel_id)，8渠道61键。
- 理想应对：只更新已授权键的weight，实际前weight准确持久备份，事务行数断言，回滚条件保护，后验一致。
- 当前/依据：运行的是外部`/workspace/ops/channel-weight-only/run_round.py`；S4报告它具备上述保护；原仓`apply/apply.go:64–83`仍写weight+priority且同步整渠道abilities，不能拿它替换生产入口。
- 差距：本次只写经验/工具/回归，不重测外部PG和回滚；仓库主算法与生产安全写回是不同层。
- 验收：未来改writer要用隔离PG验证精确复合键、事务失败原子性、写后不一致与条件恢复，不能仅MemStore。
- 状态/关联：guard-external；原runner、config、固定hash完全不动。

## CASE-AW-012：缺TTFT与远程指标未接

- 场景/证据：S1/S2静态；`probe/remote_site.go:54,60,97–98`只允许旧渠道集合，#73不在；未读取任何真实凭据。
- 理想应对：缺值显式unknown，端到端速率不冒称生成TPS；远程应有明确且一致的stream/TPS来源，先候选验证。
- 当前/依据：remote Site不输出streaming，`probe/live.go:366–373`不补分类；本地分类器不能等同实际remote。远程probe无TPS输出；日志TPS是另一来源，评分乘子关闭。`decide/score.go:222–254`首字缺失可回退30秒/日志混合；原业务称frt不可靠与配置使用它冲突。
- 差距：不能用缺失streaming证明缓冲，也不能杜撰远程TPS；启用更多远程渠道不是本次授权。
- 验收：未来补协议契约/合成解析测试，再设计一致分类；不直接扩大allowlist或live-probe。
- 状态/关联：known-gap，核心行为仅候选。

## CASE-AW-013：软上限优先

- 场景/证据：S1/S2，历史softcap包括已过时对象；不证明这些对象当前存在或可碰。
- 理想应对：人工有效softcap作为优先约束；任何恢复、慢速规则或人工覆盖都须明确权限，而非借算法绕过。
- 当前/依据：`decide/caps.go:21–87`、`decide/settle.go:84–93`；strict允许无fault的operator cap优先；但最终人工final/override在cap后，属于单独操作边界，不是统一终态强校验。
- 差距：不能把“系统cap不降”套到operator cap；也不能仅凭softcap字段存在扩大渠道范围。
- 验收：现有`TestActiveCapsKind`/`TestSettleCutNeedsFault`；后续新增全链软上限覆盖矩阵。
- 状态/关联：documented-current，不变更软上限。

## CASE-AW-014：AZY78–80质量淘汰不恢复

- 场景/证据：S5本轮明确边界；此前只读交接称旧#78已不存在。AZY标签与当前实际对象映射未再查，不能从名字推测生产状态。
- 理想应对：质量淘汰与临时低权/故障观察分开；明确禁止恢复对象，只有新的明确授权和独立验收才能重新进入候选。
- 当前/依据：当前外部writer只允许8渠道61键，不含这些淘汰标签；原Go只按status/model/exclude筛选，旧toggle有disabled测试/恢复规划，因此不能直接启用旧toggle或用fixture名恢复它们。
- 差距：原仓没有完整的运营淘汰策略注册表；本次不去修改live范围或推断ID。
- 验收：未来离线fixture加入范围外目标拒绝、缺失对象不补建、停用不启用；本经验本绝不是新allowlist。
- 状态/关联：prohibited-by-current-scope，不恢复、不探测；首次经验本文档提交。

## CASE-AW-015：CC 未达 30/3/3% 时不执行系统恢复期限幅

- 场景/日期/证据：2026-11-21 02:21:56 轮 `20261121-022156+0800`（截点后第 1 个计入轮，verified_noop）。算法窗 CC #72：ChanStat N=11、Err=2、Eff=2、Rate=2/11=.1818（注记 18.2%）；评分窗 n60=15、trust≈0.28；old=system=20。算法注记：策略 0→20，系统上限 3（信任 0.28 < 0.3且 1h 样本 15 < 20，恢复期限幅）不执行，因未达降权门槛（真实≥30、错误≥3、≥3%）。监测窗是另一套去重口径，不能把监测样本直接代入本条算法窗。S3。
- 理想应对（设计目标）：小样本高比例错误既不能当成已证明的容量故障而立即降到 0 或 3，也不能被误读成渠道已确认健康；恢复期限幅与 `cut_needs_fault` 的交互应可解释。是否要对未达门槛的高比例小样本另设观察策略，需反事实回放后才能作为候选规则，不是已批准的新降权口径。
- 当前真实处理/依据：`decide/settle.go:26–29,84–93`。CC 打开 `settle.cut_needs_fault` 时，系统 cap（trust / newcomer / small-sample / capacity）在未达 bad 且当前权重大于 cap 时，不得把渠道压到 cap 以下；operator soft_caps 仍生效。bad 需 N≥30、Eff≥3、Rate≥.03。本样本 Rate 已高于 3%，但 N=11、Eff=2 未达标；亦未达 extreme 默认（N≥20 且 Rate≥.20）。策略目标虽为 0，先被「无渠道级错误证据则不降」拉回 20，随后 trust cap 3 因上述 strict 规则不执行，终态维持 20。GPT 未开该开关时，同形状会执行 trust cap 到 3（现有 `TestSettleCutNeedsFault` 对照）。触发 cap 的「1h 样本 15」来自 n60，与 ChanStat N=11 不是同一窗口（见 CASE-AW-005）。
- 差距：易把 18.2% 或「策略 0」直接理解成应降到 0，或应执行上限 3；也易把监测窗与算法窗、n60 与 ChanStat 混用。现规则把「证据不足不降」放在系统恢复期限幅之前，这一保守选择是否过宽仍待验证，本次不改门槛或开关。
- 测试/验收：`decide/casebook_test.go:TestCasebookTrustCapHeldWithoutFault`，合成 N=11/Err=2/Eff=2 形状：CC 维持 20 且注记含「不执行」；GPT 同形状执行 cap 到 3；达 30/3/3% 后门限允许系统 cap 生效。只用虚构聚合，无真实 token/日志。
- 状态/关联：本次新增并离线执行；锁定现有 v5.01 行为，不改变阈值、开关或生产运行版本。关联本轮候选提交。


## CASE-AW-016：小样本失去首位资格后，次位渠道可被顶上并从滞回权重恢复

- 场景/日期/证据：2026-11-21 03:06:43 轮 `20261121-030643+0800`（截点后第 2 个计入轮，verified_write）。GPT #23 渠道权重 18→20，8 个精确 abilities 同步 18→20。对照上一计入轮 `20261121-022156+0800`：#32 评分窗 n60=22（≥ min_n_rank1=10）可排第一，#23 分数比约 0.88 落入 close（策略 16），raise_clean 关闭且无故障，定档 16→18 维持 18（见 CASE-AW-008）。本轮 #32 n60=7（<10 且无探测成功），不得排第一，由 #23 顶上；#23 策略目标 20，算法窗 ChanStat N=297、Err=0、Eff=0，N≥30 且错误率低于升权门槛，接受策略升权 18→20。#32 被顶下后策略 16，无故障定档维持 20。监测窗是另一口径，不能直接代入。S3。
- 理想应对（设计目标）：小样本高分不得长期占据首位以至于压制样本充足渠道的恢复；同时升权仍需足够干净样本，避免仅因“顶上”就把不足 30 的渠道一步拉到 20。是否调整 min_n_rank1、或在顶上后改写平手判定，需反事实回放后才能作为候选规则，不是已批准的新门槛。
- 当前真实处理/依据：`decide/decide.go` 的 EligibleRank1 判定与顶上逻辑（约 123、158–166 行），公式为 not (n60 < min_n_rank1 and probe_ok < 1)，配置 min_n_rank1=10；不合格的当前第一名被下一个合格渠道顶上并写注记。`decide/strategy.go` 按调整后的 order 给首位 top_w=20，次位按分数比 close_ratio=0.85 得 close（16）。`decide/settle.go`：策略上调且 N≥min_real_n、Rate<up_max_err 时接受升权（本例刚好 +2 步长）；策略下调且无 bad 时不降（raise_clean=false 也不因干净而自动回爬）。n60 与 ChanStat N 不是同一窗口（见 CASE-AW-005）。
- 差距：易把“无故障维持 18”理解成永远不能回到 20，或把本轮恢复理解成 raise_clean 已开启。也易把监测窗或 ChanStat N 误当成 min_n_rank1 的判定样本。上一轮 #32 n60=22 仍有首位资格，说明门槛是 10 不是 30。另：仅有两个渠道且高分者被取消首位资格时，次位分数成为新 top 后，原高分者仍高于 tie_ratio×新 top，可能走全员平分（w_total/n，本配置为 9）而不是给新第一名 top_w，settle 再把 9 拉回 18，恢复不会发生。现网本轮有 5 个渠道且存在低于门槛的分数，因而没有触发该分支。是否要在顶上后排除不合格第一名再判平手，仅作为候选设计。
- 测试/验收：`decide/casebook_test.go:TestCasebookRank1PromotionRecover`。合成相对分数使次位处于 close 带，并另设更低分第三渠道以避免平分：n60=7 时顶上并 18→20；n60=22 时保持第一、次位策略 16 并维持 18；两渠道同形则锁定当前全员平分后维持 18；另锁“策略已到 20 但 ChanStat N<30 时不加”。只用虚构聚合，无真实 token/日志。
- 状态/关联：本次新增并离线执行；锁定现有 v5.01 行为，不改变 min_n_rank1、raise_clean、平手判定或生产运行版本。关联本轮候选提交。
## 后续记录规则

每轮列候选、是否修改、离线测试、关联commit、候选版本、实际运行版本；无安全改进则明确“未修改及原因”。每4个唯一真实run_dir复核全部案例状态与新证据，无新增也写结论；不强迫改算法。历史7轮只是种子，不占第1–4轮计数。详见工作约定与外部state，不能因切分支重置。
