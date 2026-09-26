// Package hotreload 为生成的配置提供热更能力（手写代码，gentable 不会覆盖）。
//
// Manager[C] 是泛型配置管理器，C 为各端生成的 Config 类型:
//
//	serverMgr, _ := hotreload.New("data/server", server.Load)
//	clientMgr, _ := hotreload.New("data/client", client.Load)
//
// 语义:
//   - Get() 原子返回当前 Config 快照，业务侧无锁读取
//   - Reload() 整体构建新 Config 再原子替换——加载失败时旧配置继续生效；
//     多次并发 Reload（外部调用与 watcher 同时触发）内部互斥串行化
//   - StartWatch() 后台轮询 JSON 文件 mtime，变化即自动 Reload
//
// 线上环境可把「轮询 mtime」换成配置中心推送 / 文件下发通知，调用 Reload() 即可。
package hotreload

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Manager 配置管理器（热更安全），C 为生成的 Config 类型
type Manager[C any] struct {
	dir      string
	load     func(dir string) (*C, error) // 即生成代码里的 LoadAuto
	cur      atomic.Pointer[C]
	mu       sync.Mutex   // 保护 mtimes
	reloadMu sync.Mutex   // 串行化 Reload（外部调用与 watcher 可能并发触发）
	cbMu     sync.RWMutex // 保护 OnReload 回调的读取（WithOnReload 设置，watcher goroutine 读取）
	mtimes   map[string]time.Time
	stopCh   chan struct{}
	stopOnce sync.Once

	// OnReload 热更回调（成功时 err 为 nil，失败时旧配置继续生效）。可选。
	//
	// 必须在 StartWatch 之前设置（StartWatch 之后直接赋值是数据竞争）；
	// 推荐改用 New 的 WithOnReload 选项，天然并发安全。
	OnReload func(cfg *C, err error)
}

// Option New 的可选参数
type Option[C any] func(*Manager[C])

// WithOnReload 设置热更回调（等价于在 StartWatch 前给 OnReload 字段赋值，但并发安全）
func WithOnReload[C any](fn func(cfg *C, err error)) Option[C] {
	return func(m *Manager[C]) { m.setOnReload(fn) }
}

// New 加载 dir 下的全部 JSON 配置。首次加载失败直接返回错误。
func New[C any](dir string, load func(dir string) (*C, error), opts ...Option[C]) (*Manager[C], error) {
	m := &Manager[C]{
		dir:    dir,
		load:   load,
		mtimes: map[string]time.Time{},
		stopCh: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(m)
	}
	if err := m.Reload(); err != nil {
		return nil, err
	}
	return m, nil
}

// Get 返回当前配置快照。快照在整个生命周期内只读，业务可安全持有。
func (m *Manager[C]) Get() *C {
	return m.cur.Load()
}

// Reload 重新加载并原子替换配置。失败时保留旧配置并返回错误。
// 并发调用安全：内部互斥串行化，后到者基于最新文件状态再加载一次。
func (m *Manager[C]) Reload() error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	cfg, err := m.load(m.dir)
	m.snapshot() // 成败都记录 mtime，避免失败时每个 tick 重复报错刷屏
	if err != nil {
		return err
	}
	m.cur.Store(cfg)
	return nil
}

// StartWatch 启动后台轮询，interval 建议 500ms~2s。
func (m *Manager[C]) StartWatch(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				if !m.changed() {
					continue
				}
				err := m.Reload()
				if fn := m.getOnReload(); fn != nil {
					fn(m.Get(), err)
				}
			}
		}
	}()
}

// StopWatch 停止后台轮询
func (m *Manager[C]) StopWatch() {
	m.stopOnce.Do(func() { close(m.stopCh) })
}

// getOnReload 读取热更回调（持锁，配合 WithOnReload 并发安全）
func (m *Manager[C]) getOnReload() func(*C, error) {
	m.cbMu.RLock()
	defer m.cbMu.RUnlock()
	return m.OnReload
}

func (m *Manager[C]) setOnReload(fn func(*C, error)) {
	m.cbMu.Lock()
	defer m.cbMu.Unlock()
	m.OnReload = fn
}

func (m *Manager[C]) changed() bool {
	cur := scanDir(m.dir)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(cur) != len(m.mtimes) {
		return true
	}
	for name, mt := range cur {
		if old, ok := m.mtimes[name]; !ok || !old.Equal(mt) {
			return true
		}
	}
	return false
}

func (m *Manager[C]) snapshot() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mtimes = scanDir(m.dir)
}

func scanDir(dir string) map[string]time.Time {
	out := map[string]time.Time{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if fi, err := e.Info(); err == nil {
			out[e.Name()] = fi.ModTime()
		}
	}
	return out
}
