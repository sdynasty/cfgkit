package hotreload

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeCfg struct {
	V int `json:"v"`
}

// fakeLoad 模拟生成代码的 Load: 读 dir/cfg.json；v<0 视为坏数据
func fakeLoad(dir string) (*fakeCfg, error) {
	data, err := os.ReadFile(filepath.Join(dir, "cfg.json"))
	if err != nil {
		return nil, err
	}
	var c fakeCfg
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.V < 0 {
		return nil, fmt.Errorf("bad config v=%d", c.V)
	}
	return &c, nil
}

func writeCfg(t *testing.T, dir string, v int) {
	t.Helper()
	data, _ := json.Marshal(fakeCfg{V: v})
	if err := os.WriteFile(filepath.Join(dir, "cfg.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNewAndGet(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, dir, 1)
	m, err := New(dir, fakeLoad)
	if err != nil {
		t.Fatal(err)
	}
	defer m.StopWatch()
	if got := m.Get().V; got != 1 {
		t.Errorf("Get().V = %d, 期望 1", got)
	}
}

func TestNewFailWithoutData(t *testing.T) {
	if _, err := New(t.TempDir(), fakeLoad); err == nil {
		t.Error("无数据时 New 应当报错")
	}
}

func TestReloadAndKeepOldOnFailure(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, dir, 1)
	m, _ := New(dir, fakeLoad)
	defer m.StopWatch()

	writeCfg(t, dir, 2)
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := m.Get().V; got != 2 {
		t.Errorf("Reload 后 Get().V = %d, 期望 2", got)
	}

	// 坏数据: Reload 报错，旧快照保留
	writeCfg(t, dir, -1)
	if err := m.Reload(); err == nil {
		t.Error("坏数据 Reload 应当报错")
	}
	if got := m.Get().V; got != 2 {
		t.Errorf("失败后 Get().V = %d, 期望保留旧值 2", got)
	}
}

func TestStartWatchDetectsChangeAndFailure(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, dir, 1)
	m, _ := New(dir, fakeLoad)
	defer m.StopWatch()

	events := make(chan string, 16)
	m.OnReload = func(cfg *fakeCfg, err error) {
		if err != nil {
			events <- "err:" + err.Error()
			return
		}
		events <- fmt.Sprintf("ok:%d", cfg.V)
	}
	m.StartWatch(10 * time.Millisecond)

	// 1) 正常变更 → ok:2
	writeCfg(t, dir, 2)
	waitEvent(t, events, "ok:2")

	// 2) 坏数据 → err，且不刷屏（旧配置保留）
	writeCfg(t, dir, -1)
	waitEvent(t, events, "err:")

	// 3) 修好 → ok:3
	writeCfg(t, dir, 3)
	waitEvent(t, events, "ok:3")

	if got := m.Get().V; got != 3 {
		t.Errorf("最终 Get().V = %d, 期望 3", got)
	}
}

func waitEvent(t *testing.T, ch <-chan string, prefix string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			if len(ev) >= len(prefix) && ev[:len(prefix)] == prefix {
				return
			}
			// 非目标事件（如重复 err）继续等
		case <-deadline:
			t.Fatalf("等待事件 %q 超时", prefix)
		}
	}
}

// TestWithOnReload 通过 New 的选项设置回调（推荐用法），watcher 变更应触发
func TestWithOnReload(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, dir, 1)
	events := make(chan string, 4)
	m, err := New(dir, fakeLoad, WithOnReload(func(cfg *fakeCfg, err error) {
		if err != nil {
			events <- "err"
			return
		}
		events <- fmt.Sprintf("ok:%d", cfg.V)
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer m.StopWatch()
	m.StartWatch(10 * time.Millisecond)

	writeCfg(t, dir, 7)
	waitEvent(t, events, "ok:7")
}

// TestConcurrentReloadRace 并发 Reload + watcher + Get 混合调用，跑 go test -race 验证无数据竞争。
// Reload 内部互斥串行化；全部并发结束后配置应处于一致状态。
func TestConcurrentReloadRace(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, dir, 1)
	m, err := New(dir, fakeLoad, WithOnReload(func(cfg *fakeCfg, err error) {}))
	if err != nil {
		t.Fatal(err)
	}
	defer m.StopWatch()
	m.StartWatch(time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = m.Reload()
				_ = m.Get().V
			}
		}(i)
	}
	wg.Wait()
	if got := m.Get().V; got != 1 {
		t.Errorf("最终 Get().V = %d, 期望 1", got)
	}
}
