package main

import (
	"errors"
	"testing"
	"time"
)

func TestBackoffAndJitter(t *testing.T) {
	for i := 0; i < 50; i++ {
		if d := jitter(10 * time.Second); d < 5*time.Second || d > 10*time.Second {
			t.Fatalf("jitter aralık dışı: %v", d)
		}
	}
	if jitter(0) != 0 {
		t.Error("jitter(0)")
	}
	if d := backoffFor(1, errors.New("x")); d < 2500*time.Millisecond || d > 5*time.Second {
		t.Errorf("ilk geri çekilme: %v", d)
	}
	if d := backoffFor(20, errors.New("x")); d > 60*time.Second || d < 30*time.Second {
		t.Errorf("üst sınır: %v", d)
	}
	he := &httpErr{code: 503, msg: "yoğun", retry: 45 * time.Second}
	if d := backoffFor(1, he); d < 22*time.Second || d > 45*time.Second {
		t.Errorf("Retry-After dikkate alınmadı: %v", d)
	}
}
