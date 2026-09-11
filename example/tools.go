//go:build tools

// tools.go 以标准 tools pattern 钉住 gentable 工具版本：
// go.mod 里 require 的版本即导表工具版本，全团队/CI 一致。
//
// 导表命令（见 Makefile）:
//
//	go run github.com/sdynasty/cfgkit/gentable -excel excel -code game/config -data data
package tools

import _ "github.com/sdynasty/cfgkit/gentable"
