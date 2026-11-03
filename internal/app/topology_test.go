package app

import (
	"reflect"
	"testing"
)

func TestRootCauses(t *testing.T) {
	// web(1) -> api(2) -> db(3); api(2) -> cache(4)
	deps := map[int64][]int64{1: {2}, 2: {3, 4}}

	cases := []struct {
		name   string
		status map[int64]string
		want   []int64
	}{
		{"hepsi sağlıklı", map[int64]string{1: "up", 2: "up", 3: "up", 4: "up"}, []int64{}},
		{"db düştü, zincir etkilendi", map[int64]string{1: "down", 2: "down", 3: "down", 4: "up"}, []int64{3}},
		{"iki bağımsız kök neden", map[int64]string{1: "down", 2: "down", 3: "down", 4: "offline"}, []int64{3, 4}},
		{"yalnızca uç servis düştü", map[int64]string{1: "down", 2: "up", 3: "up", 4: "up"}, []int64{1}},
		{"bilinmeyen durum kök neden sayılmaz", map[int64]string{1: "unknown", 2: "unknown", 3: "unknown", 4: "unknown"}, []int64{}},
	}
	for _, c := range cases {
		if got := rootCauses(c.status, deps); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRootCausesCycleDoesNotHang(t *testing.T) {
	deps := map[int64][]int64{1: {2}, 2: {1}}
	// Döngüde sonsuz döngüye girmemeli; sonuçta kök neden bulunamaması kabul edilir.
	_ = rootCauses(map[int64]string{1: "down", 2: "down"}, deps)
}
