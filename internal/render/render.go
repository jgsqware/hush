package render

import "github.com/jgsqware/hush/internal/agg"

// Renderer prints aggregator items.
type Renderer interface {
	Render(items ...agg.Item) error
	Close() error
}

// Live is implemented by renderers that show what is running right now.
type Live interface {
	SetActivity(agg.Activity) error
	SetSection(string) error
	Tick() error
}

var (
	_ Renderer = (*Plain)(nil)
	_ Renderer = (*CI)(nil)
	_ Renderer = (*TTY)(nil)
	_ Live     = (*TTY)(nil)
)
