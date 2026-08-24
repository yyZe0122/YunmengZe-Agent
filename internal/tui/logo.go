package tui

import (
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/version"
)

const logoMinWidth = 60

var ymzBlock = []string{
	`█   █  █▀▄▀█  █████`,
	` █ █   █ █ █     █`,
	`  █    █   █    █`,
	`  █    █   █   █`,
	`  █    █   █  █████`,
}

func displayVersion() string {
	v := strings.TrimSpace(version.Version)
	if v == "" || v == "0.0.0-dev" {
		return ""
	}
	return v
}

func renderLogo(width int) string {
	var b strings.Builder
	if width >= logoMinWidth {
		for i, line := range ymzBlock {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(styleTitle.Render(line))
		}
		b.WriteByte('\n')
	}
	b.WriteString(styleTitle.Render("ymz"))
	if ver := displayVersion(); ver != "" {
		b.WriteString("  " + styleMuted.Render(ver))
	}
	return b.String()
}
