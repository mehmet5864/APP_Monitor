//go:build !linux && !windows

package main

// Linux ve Windows dışındaki sistemlerde yalnızca "agent çalışıyor" sinyali gönderilir.
type collector struct{}

func newCollector() *collector { return &collector{} }

func (c *collector) collect() []metric { return nil }

func processCounts() map[string]int { return map[string]int{} }
