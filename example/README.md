# cfgkit example — 新项目起步模板

把本目录整个拷贝到你的项目里，就是一个可用的配置系统骨架。

## 目录说明

```
example/
├── go.mod              # require cfgkit + tools pattern（本地联调用了 replace ../，拷走后删掉）
├── tools.go            # 钉住 gentable 版本的标准 tools pattern
├── Makefile            # make template / export / server / bin
├── tools/make_template.py   # 生成示例 Excel（实际项目由策划维护 excel/，可删）
├── excel/              # 策划维护的 Excel（唯一编辑入口）
├── game/config/
│   ├── client/         # [生成] package client（cs+c 字段，含内嵌 data/）
│   ├── server/         # [生成] package server（cs+s 字段，含内嵌 data/）
│   └── (hotreload 来自 github.com/sdynasty/cfgkit/hotreload)
├── data/               # [生成] 外部 JSON（开发热更调试 / 线上覆盖目录）
└── server/             # 最小示例服务端
```

## 快速开始

```bash
make export          # 导表（首次已生成好，可直接跑）
go run ./server      # 示例服务端：常驻 + 热更监听
make bin             # 单二进制部署产物 bin/server（配置 go:embed 内嵌）
```

## 拷到自己项目后要改的三处

1. `go.mod`：改 module 名；**删掉 `replace` 行**，然后
   `go get github.com/sdynasty/cfgkit@v0.1.0`
2. `server/main.go`：import 路径里的 `github.com/sdynasty/cfgkit/example` 换成你的 module 名
3. `excel/`：换成你的表（格式规范见仓库根 README）

## 日常流程

- 策划改 `excel/*.xlsx` → 提交
- 程序/CI 跑 `make export` → 提交生成产物（`game/config/`、`data/`）
- 服务端 `hotreload.New("data/server", server.LoadAuto)` + `StartWatch`：
  平时吃二进制内嵌配置；往覆盖目录推整套新 JSON 即热更，删掉即回落内嵌
