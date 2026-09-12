# cfgkit

[![CI](https://github.com/sdynasty/cfgkit/actions/workflows/ci.yml/badge.svg)](https://github.com/sdynasty/cfgkit/actions/workflows/ci.yml)

游戏配置系统工具包：**策划改 Excel → 导表校验 → 生成类型安全的 Go 代码 + JSON 数据 → 运行时热更**。

| 组件 | 形态 | 说明 |
|---|---|---|
| `gentable` | CLI（`go run`/`go install`） | 读 Excel，校验后按端生成 Go 代码与 JSON |
| `hotreload` | Go 库（零依赖） | 泛型配置管理器：原子快照 + mtime 轮询热更 |
| `example/` | 起步模板 | 拷走即是完整可跑的接入示例 |

## 核心特性

- **类型系统**：`int/int64/float/bool/string`、`enum<X>`、`ref<X>`（跨表引用校验）、`struct<X>`（内联结构体）、`list<T>`、`map<K,V>`，可嵌套（`list<struct<Drop>>`）
- **索引**：`#uniq` / `#index` 后缀，生成 `Get` / `GetByXxx` O(1) 查询方法
- **分端导出**：表头第 4 行 `cs/c/s/-` 标记，一次导表产出 client/server 两套独立的包与数据，字段差异编译期保证
- **单二进制部署**：JSON 经 `go:embed` 编译进二进制；配合覆盖目录实现「不发版热更」
- **导表校验**：主键唯一、枚举合法、ref 存在、struct 字段必填、循环嵌套检测——错误信息精确到 Excel 文件/sheet/行/列，策划可直接照改
- **diff 摘要**：每次导表对比上次产物，按端输出 +行/-行/~字段级变更/schema 变更/定义变更，可 `-diff-out` 存档供 CI/PR 使用

## 30 秒试用（无需 clone）

```bash
mkdir mygame && cd mygame && mkdir excel
# 放入任意符合规范的 xlsx（可从 example/excel/ 拷示例），然后:
go run github.com/sdynasty/cfgkit/gentable@latest -excel excel -code game/config -data data
```

## 项目接入（5 步）

```bash
# 1. 依赖
go get github.com/sdynasty/cfgkit@v0.1.0
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

- sheet 名 = 表名 = Go 类型名，**全局唯一**；`Enum<X>` 开头为枚举表，`Struct<X>` 开头为结构体表
- 首列必须为主键（`int/int64/string`）且标记为 `cs`
- 首列 `#` 开头的行是注释行（分组用），空行跳过；单元格留空 = 零值（枚举、已填写的 struct、`#uniq` 字段除外）

### 类型表

| 类型 | 单元格写法 | Go 类型 |
|---|---|---|
| `int` / `int64` / `float` | `100` / `3.5` | `int32` / `int64` / `float64` |
| `bool` | `1/0`、`true/false`、`是/否` | `bool` |
| `string` | 任意文本 | `string` |
| `enum<ItemQuality>` | 枚举名 `White` | 生成的枚举类型（JSON 存名字） |
| `ref<Item>` | 目标表主键 `1001` | 目标表主键类型 |
| `struct<Pos>` | `x:100;y:200` | 生成的结构体（字段全必填，空格=null=零值） |
| `list<T>` | `\|` 分隔 | `[]T` |
| `map<K,V>` | `k:v;k:v` | `map[K]V` |

类型可加索引后缀：

| 后缀 | 语义 | 生成方法 |
|---|---|---|
| `string#uniq` | 唯一索引，**不能为空**（空值导表报错——与运行时全行查重保持一致） | `GetByXxx(v) *Row` |
| `string#index` | 普通索引，可重复、可为空 | `GetByXxx(v) []*Row` |

枚举表（`EnumItemQuality`）：表头 `name/value/comment`，一行一个枚举项。
结构体表（`StructDrop`）：表头 `name/type/comment`，一行一个字段，字段类型可用全部类型（可嵌套）。

## gentable 命令行

```
gentable -excel <目录> -code <Go生成根目录> -data <外部JSON根目录> [-diff-out <文件>] [-version]

  -excel     Excel 目录（递归扫 *.xlsx，自动跳过 ~$/.~ 临时锁文件）
  -code      生成 <code>/client 与 <code>/server 两个包（含内嵌 data/）
  -data      生成 <data>/client 与 <data>/server（覆盖目录/开发调试）；空串则只出内嵌
  -diff-out  diff 摘要另存文件（CI 归档 / 贴 PR）
```

退出码非 0 = 校验失败，错误按 `文件[sheet] 第R行C列(字段)` 定位，可直接阻断 CI。

## hotreload API

```go
mgr, err := hotreload.New(dir, server.LoadAuto)  // New[C any](dir, load)
cfg := mgr.Get()                                  // *server.Config 原子快照，无锁读
mgr.StartWatch(500 * time.Millisecond)            // mtime 轮询，变化自动重建+原子替换
mgr.OnReload = func(cfg *server.Config, err error) { ... }  // 失败时旧配置继续生效
mgr.StopWatch()
```

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
| 端名固定 client/server、包名=端名 | `gentable/gencode.go` 顶部 `sides` |
| `Enum`/`Struct` sheet 前缀约定 | `gentable/parse.go` LoadExcels |
| 非 Go 客户端（C#/Lua 等） | 在 `WriteGoCode` 旁增加对应 writer，JSON 数据可直接复用 |

## 版本策略

语义化版本 + git tag（`v0.1.0` 起）。消费项目通过 go.mod 钉版本，升级节奏各自独立；
导表产物向后兼容原则：新版本生成的代码不破坏旧数据加载（`Load` 签名不变）。
