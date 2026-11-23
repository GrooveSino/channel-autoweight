# 双算法离线评审

在本机仓库 `/Users/groove/Project/code/channel-autoweight` 中运行：

```sh
go run ./cmd/autoweight --algorithm legacy-v501 --snap testdata/paired --json
go run ./cmd/autoweight --algorithm first-pass-v1 --snap testdata/paired
go run ./cmd/autoweight --compare --snap testdata/paired
go test -count=1 ./...
python3 -m unittest discover -s tools -p 'test_*.py'
```

不指定算法默认 legacy-v501。标准输出为 strength-v1 JSON：`target_strength` 是 0.000–1.000 相对强度，不是流量份额；`null` 表示弃权或排除，不能转换成零权重。旧版以配置满刻度归一；新版在有充分证据时按固定需求混合的达标率比值连续输出，可信并列取 1.000。对照同时输出 `target_delta`（新减旧）。旧版原报告须显式传 `--legacy-integer-output`（原 JSON 再加 `--json`）。该变更针对标准输出，历史轮次记录格式不变。不要省略示例中的 `--snap`：旧入口默认读取运行环境。新版及对照禁止 `--apply`。

`paired` 的虚构随机首选每渠 128 条、每用户每渠一次。两渠均完成，但 #101 用时 1 秒，#102 用时 20 秒；合成时限 5 秒。旧版输出 1.000/1.000，新版输出 1.000/0.000（后者没有一次按时完成）。当两渠可靠达标率分别为 0.950、0.817 时，新版输出 1.000/0.860。两例均非线上性能结果。

用 `--compare --snap testdata/paired --weight-scale 100` 预览整数换算，不更改授权。`integer_weights` 为原始换算，`weights_after_original_caps` 按源整数上限裁剪；池份额按实际选择路径单列。刻度 10、20、100 下强度 0.347 分别对应 3、7、35，生产原上限 20 不会因此变成 100。

`firstpass.json` 是请求级评审输入，`snap.json` 是同一采样时点的旧日志投影。截止及到达时间为 Unix 秒，体验时限与耗时为毫秒。Stratum 名称由采集端按精确模型、endpoint、任务类型、预算、输入档、路由池生成；Pool 必须完整，与实际优先级层一致。相同权重的数据库候选保留输入顺序。`Fixed` 是参与比较但不可调整的渠道；不得从渠道标签猜测权限。未配置时限、缺测过多或跨层目标冲突时弃权。分数把未知按非达标计作为下界；处置区间同时考虑未知可能成功。置信界以用户簇独立为前提，不能消除共同时段的系统性偏差。

`python3 tools/make_fixtures.py` 可重复生成全部 JSON。`case-index.json` 映射 59 个来源条目到测试；查到映射不等于该案例所有派生情形均已验收。`plumbing` 只测试历史模拟 writer 与探测豁免。旧的、依赖未跟踪快照的 Python golden 对照不再是新套件的独立 oracle；历史原件可在提交 `8c346372` 查看。

## 生产接入边界

现在可用的是单轮评审和内存执行协议测试，不是线上控制器。正式采集需提供已成熟的首选请求链、入库时间、协议可用性、分层、权重与完整候选顺序；当前旧日志没有这些字段。两个快照文件应由一个版本化导出生成，不可任意拼接。`weightonly.Model` 仅验证模拟前像、授权范围、未知闩锁、缓存纪元和回滚合同，不能替代数据库事务、跨进程锁、持久化日志或每个实例的生效证明。

接入正式适配器之前需取得脱敏 `run_round.py`、`weight_sql.py`、精确 71 键、人工策略与实际路由配置；每 10 分钟的调度由外部运行器负责。未批准新时限、分档、事故阈值与恢复预算前不写权重。旧 apply/toggle 不能作为新生产入口。
