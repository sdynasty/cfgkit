.PHONY: all template export server bin

# 完整流程: 生成示例 Excel -> 导表
all: template export

# 重建示例 Excel 模板（实际项目中 Excel 由策划维护）
template:
	python3 tools/make_template.py

# 导表（工具版本由 go.mod 钉住）
export:
	go run github.com/sdynasty/cfgkit/gentable -excel excel -code game/config -data data

# 运行示例服务端（常驻，监听热更）
server:
	go run ./server

# 单二进制部署产物（配置已 go:embed 内嵌）
bin:
	go build -o bin/server ./server
	@echo "产物: bin/server （无需携带数据文件；覆盖目录默认为 ./data/server）"
