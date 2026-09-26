# cfgkit

[![CI](https://github.com/sdynasty/cfgkit/actions/workflows/ci.yml/badge.svg)](https://github.com/sdynasty/cfgkit/actions/workflows/ci.yml)

游戏配置系统工具包：**策划改 Excel → 导表校验 → 生成类型安全的 Go 代码 + JSON 数据 → 运行时热更**。

| 组件 | 形态 | 说明 |
|---|---|---|
| `gentable` | CLI（`go run`/`go install`） | 读 Excel，校验后按端生成 Go 代码与 JSON |
| `hotreload` | Go 库（零依赖） | 泛型配置管理器：原子快照 + mtime 轮询热更 |
| `example/` | 起步模板 | 拷走即是完整可跑的接入示例 |

## 核心特性

- **类型系统**：`int/int64/float/bool/string`、`enum<X>`、`ref<X>`（跨表引用校验）、`struct<X>`（内联结构体）、`list<T>`、`map<K,V>`，可嵌套（`list<struct<Drop>>`）；struct 字段支持可选标记 `type?` / `type?=默认值`
- **索引**：`#uniq` / `#index` 后缀，生成 `Get` / `GetByXxx` O(1) 查询方法
- **分端导出**：表头第 4 行 `cs/c/s/-` 标记，一次导表产出 client/server 两套独立的包与数据（可 `-sides` 只导一端），字段差异编译期保证
- **单二进制部署**：JSON 经 `go:embed` 编译进二进制；配合覆盖目录实现「不发版热更」
- **导表校验**：主键唯一、int/枚举值 int32 范围、枚举合法、ref 存在、struct 必填字段、循环嵌套、Go 标识符冲突、JSON 文件名冲突——错误信息精确到 Excel 文件/sheet/行/列，策划可直接照改
- **diff 摘要**：每次导表对比上次产物，按端输出 +行/-行/~字段级变更/schema 变更/定义变更，可 `-diff-out` 存档供 CI/PR 使用
- **write-if-changed**：产物内容相同则不重写（不刷 mtime），下游 hotreload 不会因重复导表做无效热更；`manifest.json` 记录逐表 md5 与整体 digest

## 30 秒试用（无需 clone）

```bash
mkdir mygame && cd mygame && mkdir excel
# 放入任意符合规范的 xlsx（可从 example/excel/ 拷示例），然后:
go run github.com/sdynasty/cfgkit/gentable@latest -excel excel -code game/config -data data
```

## 项目接入（5 步）

```bash
# 1. 依赖
go get github.com/sdynasty/cfgkit@v0.2.0
```

> **国内代理提示**：若 `GOPROXY` 指向 goproxy.cn 且刚打的 tag 尚未同步（报 sumdb 404），
> 临时用 `GOPRIVATE=github.com/sdynasty/* go get ...` 直连 git，或等代理同步后重试。
> 若仓库设为私有，则所有成员都需设置 `GOPRIVATE`（并确保 git 有 GitHub 凭据）。

```go
// 2. tools.go —— 钉住导表工具版本（标准 tools pattern）
//go:build tools

package tools

import _ "github.com/sdynasty/cfgkit/gentable"
```

```makefile
# 3. Makefile
export:
	go run github.com/sdynasty/cfgkit/gentable -excel excel -code game/config -data data
```

```go
// 4. 服务端加载（内嵌为底，覆盖目录热更）
import (
    "github.com/sdynasty/cfgkit/hotreload"
    "yourmod/game/config/server"
)

mgr, err := hotreload.New("/etc/game/server", server.LoadAuto)
mgr.StartWatch(time.Second)
defer mgr.StopWatch()

cfg := mgr.Get()                 // 原子快照，只读
item := cfg.Item.Get(1001)       // 主键 O(1)
boss := cfg.Monster.GetByName("幼龙")
```

```bash
# 5. 部署: 单二进制
go build -o bin/server ./server   # 配置已内嵌，bin/server 拷走即跑
```

更完整的骨架直接拷贝 `example/` 目录（含示例 Excel、Makefile、最小服务端）。

## Excel 规范（给策划）

数据表 sheet 固定 **4 行表头**，第 5 行起为数据：

| 行 | 内容 | 说明 |
|---|---|---|
| 1 | 字段名 | 英文（字母/数字/下划线），即 JSON key 与 Go 字段来源 |
| 2 | 类型 | 见下表，可带 `#uniq`/`#index` 后缀 |
| 3 | 注释 | 中文说明，生成到 Go 代码注释 |
| 4 | 导出标记 | `cs` 双端 / `c` 仅客户端 / `s` 仅服务端 / `-` 不导出（留空默认 cs） |

- sheet 名 = 表名 = Go 类型名，**全局唯一**，必须是合法 Go 标识符（非关键字）；`Enum<X>` 开头且表头为 `name/value/comment` 的是枚举表，`Struct<X>` 开头且表头为 `name/type/comment` 的是结构体表，其余一律按数据表处理（所以数据表叫 `EnumValue`/`Structure` 没问题）
- 首列必须为主键（`int/int64/string`），标记为 `cs`，且必须在第 1 列（A 列不能标 `-`）
- 首列 `#` 开头的行是注释行（分组用），空行跳过；单元格留空 = 零值（枚举、已填写的 struct、`#uniq` 字段除外）
- `int` 与枚举值都生成 Go `int32`，导表期校验范围，越界直接报错

### 类型表

| 类型 | 单元格写法 | Go 类型 |
|---|---|---|
| `int` / `int64` / `float` | `100` / `3.5` | `int32` / `int64` / `float64` |
| `bool` | `1/0`、`true/false`、`是/否` | `bool` |
| `string` | 任意文本 | `string` |
| `enum<ItemQuality>` | 枚举名 `White` | 生成的枚举类型（JSON 存名字） |
| `ref<Item>` | 目标表主键 `1001` | 目标表主键类型 |
| `struct<Pos>` | `x:100;y:200` | 生成的结构体（整格留空=null=零值） |
| `list<T>` | `\|` 分隔 | `[]T` |
| `map<K,V>` | `k:v;k:v` | `map[K]V` |

分隔符转义（list 的 `|`、struct/map 的 `;` 与 `:`）：值里要写分隔符本身时用反斜杠——
`\\` 表示 `\`，`\|` 表示 `|`，`\;` 表示 `;`，`\:` 表示 `:`。不含反斜杠的单元格行为不变；
未列出的 `\x` 序列（如 `C:\new`）原样保留。

结构体字段的可选标记（仅 `Struct` 定义表的字段类型可用，数据表列不支持）：

| 写法 | 语义 |
|---|---|
| `int` | 必填，单元格必须填写该字段 |
| `int?` | 可选，单元格可省略（取零值） |
| `int?=5` | 可选，省略时取默认值 `5`（默认值按该类型解析，非法则导表报错） |

省略的字段在 JSON 中物化默认值/零值（Go 侧始终是值类型，不用指针）；整格留空仍是 `null`。
默认值支持全部类型（含 `enum<X>?=名字`、`list<int>?=1|2`、`ref<Item>?=1001`）。

类型可加索引后缀：

| 后缀 | 语义 | 生成方法 |
|---|---|---|
| `string#uniq` | 唯一索引，**不能为空**（空值导表报错——与运行时全行查重保持一致） | `GetByXxx(v) *Row` |
| `string#index` | 普通索引，可重复、可为空 | `GetByXxx(v) []*Row` |

枚举表（`EnumItemQuality`）：表头 `name/value/comment`，一行一个枚举项，value 生成 `int32` 常量。
结构体表（`StructDrop`）：表头 `name/type/comment`，一行一个字段，字段类型可用全部类型（可嵌套），可带 `?`/`?=` 可选标记。

## gentable 命令行

```
gentable -excel <目录> -code <Go生成根目录> -data <外部JSON根目录> [-sides client,server] [-diff-out <文件>] [-version]

  -excel     Excel 目录（递归扫 *.xlsx，自动跳过 ~$/.~ 临时锁文件）
  -code      生成 <code>/client 与 <code>/server 两个包（含内嵌 data/），
             另在根目录生成 manifest.json（版本/逐表 md5/行数/源文件 md5/整体 digest，运行时不消费）
  -data      生成 <data>/client 与 <data>/server（覆盖目录/开发调试）；空串则只出内嵌
  -sides     只生成指定端，如 -sides=server（默认 client,server 两端）
  -diff-out  diff 摘要另存文件（CI 归档 / 贴 PR）
```

退出码非 0 = 校验失败，错误按 `文件[sheet] 第R行C列(字段)` 定位，可直接阻断 CI。
内容相同的产物不会重写（不刷 mtime），结尾统计「N 个文件更新，M 个未变化」。

公式缓存告警：excelize 只能读到公式的**缓存计算值**；由脚本（openpyxl 等）生成、未经
Excel 打开计算的 xlsx 没有缓存值，读到的是空串。对「数值/布尔/枚举/#uniq 单元格为空但
含公式」的情况导表结束统一打印告警到 stderr（不阻断），建议用 Excel 打开重存。

## hotreload API

```go
mgr, err := hotreload.New(dir, server.LoadAuto)  // New[C any](dir, load, opts ...Option)
cfg := mgr.Get()                                  // *server.Config 原子快照，无锁读
mgr.StartWatch(500 * time.Millisecond)            // mtime 轮询，变化自动重建+原子替换
mgr.StopWatch()
```

热更回调（成功时 err 为 nil，失败时旧配置继续生效）两种设法：

```go
mgr, _ := hotreload.New(dir, server.LoadAuto,
    hotreload.WithOnReload(func(cfg *server.Config, err error) { ... }))  // 推荐，并发安全
mgr.OnReload = func(cfg *server.Config, err error) { ... }                // 兼容写法：必须在 StartWatch 之前赋值
```

`Reload()` 内部互斥串行化，外部调用与 watcher 并发触发安全。

生成的加载函数：`LoadEmbedded()`（只读内嵌）/ `Load(dir)`（只读外部）/ `LoadAuto(dir)`（外部优先回落内嵌）/ `HasExternal(dir)`（来源探测）。

## CI 建议

```bash
go run github.com/sdynasty/cfgkit/gentable -excel excel -code game/config -data data -diff-out /tmp/diff.txt \
  && git diff --exit-code game/config data \
  || { echo "导表失败或生成产物未提交"; exit 1; }
```

1. 导表本身即全量校验，失败阻断合入
2. `git diff --exit-code` 确保「改了 Excel 必须重新导表提交产物」
3. `/tmp/diff.txt` 作为构建产物贴 PR，review 策划改动一目了然

## 已知耦合点（按需改造）

| 耦合点 | 位置 |
|---|---|
| 端名固定 client/server、包名=端名（`-sides` 只控制导哪几端） | `gentable/gencode.go` 顶部 `allSides` |
| `Enum`/`Struct` sheet 前缀 + 表头嗅探 | `gentable/parse.go` classifySheet |
| 非 Go 客户端（C#/Lua 等） | 在 `WriteGoCode` 旁增加对应 writer，JSON 数据可直接复用 |

## 版本策略

语义化版本 + git tag（`v0.1.0` 起）。消费项目通过 go.mod 钉版本，升级节奏各自独立；
导表产物向后兼容原则：新版本生成的代码不破坏旧数据加载（`Load` 签名不变）。
