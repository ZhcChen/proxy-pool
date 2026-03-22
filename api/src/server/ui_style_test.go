package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIStylesInstanceActionCompactColumnWidth(t *testing.T) {
	cssPath := filepath.Join("..", "web", "public", "style.css")
	body, err := os.ReadFile(cssPath)
	if err != nil {
		t.Fatalf("读取样式文件失败: %v", err)
	}

	text := string(body)
	if !strings.Contains(text, ".instance-actions-col-compact,\n.instance-actions-cell-compact {\n  width: 640px;\n  min-width: 640px;\n  max-width: 640px;\n}") {
		t.Fatalf("桌面端实例操作列紧凑宽度未提升到 640px")
	}
	if !strings.Contains(text, ".instance-actions-col-compact,\n  .instance-actions-cell-compact {\n    width: 470px;\n    min-width: 470px;\n    max-width: 470px;\n  }") {
		t.Fatalf("窄屏实例操作列紧凑宽度未提升到 470px")
	}
}
