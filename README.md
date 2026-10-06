# coverage-report-engine-go

本地测试覆盖率采集与报告引擎。输入来自本机测试命令产出的 Go coverprofile
文本流，不依赖任何外部服务或真实凭据。核心能力：采集校验、多运行/多分片
合并、增量采集、并发分片容错、阈值门禁与确定性报告。

包路径：`github.com/shikejing/coverage-report-engine-go/cover`

## 采集语义

- 输入为 Go coverprofile 文本格式：首行 `mode: set|count|atomic`，之后每行
  `文件:起始行.起始列,结束行.结束列 语句数 命中次数`。
- `Engine.Ingest` / `ParseProfile` 在解析时做严格校验，以下情况会被**明确
  拒绝**并给出可区分的原因（`*RejectError.Reason`），绝不会被当成零命中丢弃：

  | 原因 | 含义 |
  |---|---|
  | `missing_mode_header` | 缺少或错误的 mode 头 |
  | `unknown_mode` | 不支持的计数模式 / 合并时模式冲突 |
  | `malformed_line` | 行格式损坏（位置、语句数、命中数非法） |
  | `unknown_source` | 文件路径逃逸出声明的模块根（来源不明） |
  | `conflicting_block_geometry` | 同一块在不同来源中几何信息冲突 |
  | `bad_magic` / `incompatible_version` / `corrupt_payload` | 存储层拒绝原因 |

- 文件路径统一归一化为相对模块根的斜杠路径，多包与嵌套目录按目录归属包。

## 合并规则

- `Merger.Import` 按块（起始/结束行列 + 语句数）累加命中次数；加法可交换，
  因此**导入顺序不影响最终结果**。
- 每个 profile 携带 `RunID`；同一 `RunID` 重复导入是幂等 no-op，
  **不会重复计数**。RunID 随存储持久化，重载后仍然有效。
- 首个导入的 profile 固定计数模式，后续模式不匹配的 profile 被拒绝。
- 分片失败/超时的文件进入 `MissingRange` 列表（含原因），不计入语句总数也
  不计为零命中，结果标记 `Complete=false`。

## 增量采集

- `Incremental` 以基线 `Merger` 中的内容哈希（`SetContentHash`）为准：
  `ChangedFiles` 把文件分为已变化/未变化；只需对变化文件重新采集，
  `Build` 自动复用基线中未变化文件的覆盖数据并刷新哈希。
- 同一输入下增量结果与全量重建**逐文件一致**（有测试保证）。

## 并发分片

- `PlanShards` 用「排序 + FNV 哈希取模 + 片内排序」生成分片计划，同一输入
  任何调用顺序下计划完全一致。
- `CollectShards` 并发执行各分片（`MaxParallel` 限制并发度，每片独立超时），
  所有分片结束后才合并：取消或超时不会留下半合并状态——整体取消时返回的
  Merger 为 nil 且错误原因为 `canceled`。

## 阈值门禁

```go
Thresholds{
    Overall:  80,                       // 全局最低覆盖率（百分比）
    Packages: map[string]float64{"pkg": 90}, // 按包覆盖
    TopN:     5,                        // 列出的最差位置数量
}
```

- `Check` 返回 `*GateError`：`Violations` 按作用域区分（`overall` /
  `package:<路径>`），含要求值与实际差值；`Worst` 按「未覆盖语句数降序、
  回归差值升序、路径升序」确定排序，传入基线时给出每个文件的回归 `Delta`。
- 相同输入下判定结果完全可复现。

## 报告

- `WriteReport(w, res, FormatJSON)`：机器可读；`FormatText`：人类可读，
  支持 包 → 文件 下钻。
- 输出逐字节确定：所有集合先排序再输出，百分比固定保留两位小数。
- 报告按包流式写出，内存占用不随覆盖数据总量线性膨胀。

## 存储格式与兼容

- 布局：`magic(4B "COVR") | version(2B) | JSON payload`。
- 当前版本 v2（含 RunID 与内容哈希）；v1（仅文件覆盖）声明兼容，
  `Load` 仍可正确读取并与新数据合并。
- 版本超出 `[MinVersion, Version]` 的数据在读取阶段即以
  `incompatible_version` 拒绝；magic 错误与 payload 损坏分别报
  `bad_magic` / `corrupt_payload`。
- 采集入口（`Ingest`/`ParseProfile`）与报告调用（`WriteReport`）签名不随
  格式演进变化，调用方无需迁移。

## 测试

```sh
go test -race ./...
```

覆盖：采集校验、多分片合并与重复导入、增量与全量一致性、并发分片失败/
超时/取消、阈值门禁、格式兼容（v1 读取、不兼容版本拒绝）、报告字节级
确定性。
