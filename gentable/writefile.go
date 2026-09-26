package main

import (
	"bytes"
	"os"
)

// ------------------------------------------------------------ 产物写入（write-if-changed）
//
// 下游 hotreload 按 JSON 文件 mtime 触发热更：导表若无条件重写全部产物，
// 每次导表都会让所有服务做一次无效热更。因此写入前比较内容，相同则跳过
// （不刷 mtime），并统计更新/未变化数量供导表摘要展示。

// writeCounter 产物写入统计
type writeCounter struct {
	updated   int // 实际写入（新增或内容变化）
	unchanged int // 内容相同跳过
}

// writeIfChanged 与磁盘内容相同则不写（保持原 mtime），否则覆盖写入
func (w *writeCounter) writeIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		w.unchanged++
		return nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	w.updated++
	return nil
}
