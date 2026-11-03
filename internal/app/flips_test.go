package app

import (
	"testing"

	"appmon/internal/browser"
)

func res(items ...string) browser.Result {
	return browser.Result{Steps: []browser.StepResult{{Name: "a", OK: true, Items: items}}}
}

// Gelip giden öğe iki değişimden sonra dinamik sayılır; gerçek (kalıcı) değişiklik bir kez bildirilir.
func TestToOutsVolatile(t *testing.T) {
	prev := map[int][]string{0: {"h:A"}}
	var flips map[int]map[string]int
	_, ch, flips := toOuts(res("h:A", "btn:X"), prev, flips) // X eklendi (1. değişim)
	if ch != 1 {
		t.Fatalf("ilk ekleme bildirilmeli, değişim=%d", ch)
	}
	prev = map[int][]string{0: {"h:A", "btn:X"}}
	_, ch, flips = toOuts(res("h:A"), prev, flips) // X kayboldu (2. değişim)
	if ch != 1 {
		t.Fatalf("ikinci değişim hâlâ bildirilir, değişim=%d", ch)
	}
	prev = map[int][]string{0: {"h:A"}}
	outs, ch, _ := toOuts(res("h:A", "btn:X"), prev, flips) // X tekrar: artık dinamik
	if ch != 0 || outs[0].Ignored != 1 {
		t.Fatalf("dinamik öğe yok sayılmalı, değişim=%d ignored=%d", ch, outs[0].Ignored)
	}
}
