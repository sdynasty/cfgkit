package server

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LoadAuto 逐表 overlay 语义: 目录里有哪些表就用哪些，缺的回落内嵌

func TestLoadAutoEmptyDir(t *testing.T) {
	cfg, err := LoadAuto(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.DescribeSources(); got != "item=embed monster=embed" {
		t.Fatalf("空目录应全内嵌, got: %s", got)
	}
	if cfg.Item.Count() == 0 || cfg.Monster.Count() == 0 {
		t.Fatal("内嵌配置不应为空")
	}
}

func TestLoadAutoSingleFileOverlay(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "data", "server", "item.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "item.json"), src, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadAuto(dir)
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum(src)
	want := "item=external(" + hex.EncodeToString(sum[:])[:8] + ") monster=embed"
	if got := cfg.DescribeSources(); got != want {
		t.Fatalf("单文件 overlay 来源不符, want: %s, got: %s", want, got)
	}
	if cfg.Item.Count() == 0 || cfg.Monster.Count() == 0 {
		t.Fatal("overlay 后两表都应有数据")
	}
}

func TestLoadAutoCorruptFileFailsLoud(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "item.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadAuto(dir)
	if err == nil {
		t.Fatal("损坏的外部文件应报错，不允许静默回落内嵌")
	}
	if !strings.Contains(err.Error(), "item.json") && !strings.Contains(err.Error(), "Item") {
		t.Fatalf("错误应指明是哪张表: %v", err)
	}
}
