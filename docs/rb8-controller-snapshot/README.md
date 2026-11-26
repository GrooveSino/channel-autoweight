# RB8 本地控制器快照（不含生产密文）

这是 2026-11-26 pt80u 修订后的独立观察层控制器逻辑快照，供 `rb8/main-v2-20261125` 分支同步。

- 正式运行入口仍是云电脑上的 `/workspace/ops/rb8-v2-20261125/rb8.py`。该目录的 `weight_sql.py`（含生产库身份）、应用 config、71-key 授权清单、STATE 与 runs/export 日志故意不放入本公共仓库。
- 因此本目录不能单独对生产调权，也不包含可连接生产库的身份校验文件。
- `test_rb8.py` 覆盖 unknown 不再单独禁升、救援仍只阻上调、风险帽与 ±6；完整隔离 PostgreSQL 验收与 `weight_sql.py` 留在正式部署目录。
- 同步的 `rb8.py` 只含 SSH 主机别名与只读导出 SQL，不含密钥、token 或 system_identifier。
