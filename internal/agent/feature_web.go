package agent

import (
	"github.com/yyZe0122/yunmengze-agent/internal/tools"
)

func (e *Engine) registerWeb(r *tools.Registry) {
	if !e.Config.WebEnabled() {
		return
	}
	config := tools.WebConfig{Backend: e.Config.WebSearchBackend()}
	if e.Config.Web != nil {
		config.SearxNGURL = e.Config.Web.SearxNGURL
		config.TavilyKey = e.Config.Web.TavilyKey
	}
	r.Web(config)
}
