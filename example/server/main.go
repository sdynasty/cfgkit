// server 是接入 cfgkit 的最小示例（仅 server 端）:
//
//	make export          # 导表（生成 game/config/{client,server} 与 data/）
//	go run ./server      # 常驻，监听 data/server 热更
//	go run ./server -once
//	make bin             # 单二进制部署产物（配置 go:embed 内嵌）
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sdynasty/cfgkit/example/game/config/server"
	"github.com/sdynasty/cfgkit/hotreload"
)

func main() {
	once := flag.Bool("once", false, "打印一次后退出")
	dataDir := flag.String("data", "data/server", "外部覆盖目录（有 JSON 用外部，否则用二进制内嵌）")
	flag.Parse()

	mgr, err := hotreload.New(*dataDir, server.LoadAuto)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载配置失败:", err)
		os.Exit(1)
	}
	if server.HasExternal(*dataDir) {
		fmt.Println("配置来源: 外部覆盖目录", *dataDir)
	} else {
		fmt.Println("配置来源: 二进制内嵌 (go:embed)")
	}

	dump(mgr.Get())
	if *once {
		return
	}

	mgr.OnReload = func(cfg *server.Config, err error) {
		if err != nil {
			fmt.Printf("\n[%s] ✗ 热更失败，继续使用旧配置: %v\n", time.Now().Format("15:04:05"), err)
			return
		}
		fmt.Printf("\n[%s] ✓ 热更成功:\n", time.Now().Format("15:04:05"))
		dump(cfg)
	}
	mgr.StartWatch(500 * time.Millisecond)
	defer mgr.StopWatch()

	fmt.Println("\n监听", *dataDir, "中（500ms）。改 Excel 后 make export，或直接改该目录 JSON。Ctrl+C 退出")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}

func dump(cfg *server.Config) {
	fmt.Printf("\n== Item（%d 行）==\n", cfg.Item.Count())
	for _, it := range cfg.Item.List() {
		fmt.Printf("  %d %-6s 品质=%-6s 售价=%d\n", it.Id, it.Name, it.Quality, it.Price)
	}
	fmt.Printf("\n== Monster（%d 行）==\n", cfg.Monster.Count())
	for _, m := range cfg.Monster.List() {
		fmt.Printf("  %d Lv%-2d %-5s pos=(%d,%d) 掉落:", m.Id, m.Level, m.Name, m.BornPos.X, m.BornPos.Y)
		for _, d := range m.DropGroup { // list<struct<Drop>>，Drop.Item 为 ref<Item>
			if it := cfg.Item.Get(d.Item); it != nil {
				fmt.Printf(" %s x%d@%.0f%%", it.Name, d.Count, d.Rate*100)
			}
		}
		fmt.Println()
	}
}
