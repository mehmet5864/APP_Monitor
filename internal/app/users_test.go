package app

import "testing"

func TestPasswordHash(t *testing.T) {
	h, err := hashPassword("cok-gizli-parola")
	if err != nil {
		t.Fatal(err)
	}
	if !checkPassword("cok-gizli-parola", h) || checkPassword("yanlis", h) || checkPassword("x", "bozuk") || checkPassword("x", "") {
		t.Fatal("parola doğrulaması hatalı")
	}
	h2, _ := hashPassword("cok-gizli-parola")
	if h == h2 {
		t.Fatal("tuz her seferinde farklı olmalı")
	}
}

func TestAnomalyZ(t *testing.T) {
	// medyan 50, MAD 3 -> sigma = max(4.45, %10 medyan = 5) = 5; şimdiki 120 -> 14
	if z := anomalyZ(120, 50, 3); z < 13.9 || z > 14.1 {
		t.Fatalf("z = %v", z)
	}
	if z := anomalyZ(51, 50, 3); z > 1 || z < -1 {
		t.Fatalf("normal değer yüksek skor aldı: %v", z)
	}
	// MAD=0 (sabit seri): küçük sapma devasa skor üretmemeli
	if z := anomalyZ(50.5, 50, 0); z > 0.2 {
		t.Fatalf("sabit seride küçük sapma: %v", z)
	}
	if z := anomalyZ(80, 50, 0); z < 5 {
		t.Fatalf("sabit seride büyük sapma yakalanmalı: %v", z)
	}
}
