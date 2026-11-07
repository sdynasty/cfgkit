# cfgkit example — 新项目起步模板

把本目录整个拷贝到你的项目里，就是一个可用的配置系统骨架。

## 目录说明

```
example/
├── go.mod              # require cfgkit + tools pattern（本地联调用了 replace ../，拷走后删掉）
├── tools.go            # 钉住 gentable 版本的标准 tools pattern
├── Makefile            # make template / export / server / bin
├── tools/make_template.py   # 生成示例 Excel（实际项目由策划维护 excel/main/，可删）
├── excel/
│   └── main/           # 默认 Branch 的完整 Excel；其他一级目录按 sheet 整表覆盖
├── game/config/
│   ├── client/         # [生成] package client（含内嵌 data/<branch>/）
│   ├── server/         # [生成] package server（含内嵌 data/<branch>/）
│   ├── manifest.json   # [生成] 分支/逐表 md5/整体 digest
│   └── (hotreload 来自 github.com/sdynasty/cfgkit/hotreload)
├── data/               # [生成] 外部 JSON（data/<side>/<branch>/）
└── server/             # 最小示例服务端
```

## 快速开始

```bash
make export          # 导表（首次已生成好，可直接跑）
go run ./server      # 示例服务端：常驻 + 递归监听 Branch 热更
make bin             # 单二进制部署产物 bin/server（配置 go:embed 内嵌）
```

## 拷到自己项目后要改的三处

1. `go.mod`：改 module 名；**删掉 `replace` 行**，然后
   `go get github.com/sdynasty/cfgkit@<最新版本>`
2. `server/main.go`：import 路径里的 `github.com/sdynasty/cfgkit/example` 换成你的 module 名
3. `excel/main/`：换成你的完整默认表（格式规范见仓库根 README）

## 日常流程

- 策划改 `excel/main/*.xlsx`，或在 `excel/<branch>/` 放整表覆盖 → 提交
- 程序/CI 跑 `make export` → 提交生成产物（`game/config/`、`data/`）
- 服务端用 `hotreload.New("data/server", server.LoadConfigSetAuto, hotreload.WithRecursive[server.ConfigSet]())`
- 通过 `mgr.Get().Get("main")` 或其他 Branch 名读取完整配置；空 Branch 默认 main，未知 Branch 返回错误
- 外部覆盖目录存在相同 JSON 时使用外部版本，缺失时回落该 Branch 的内嵌版本
