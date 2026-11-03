//go:build !linux

package main

// Linux dışındaki sistemlerde şimdilik yalnızca "agent çalışıyor" sinyali gönderilir.
// Windows ve macOS ana makine metrikleri sonraki sürümde eklenecek.
type collector struct{}

func newCollector() *collector { return &collector{} }

func (c *collector) collect() []metric {
	return []metric{{"agent.up", 1}}
}
