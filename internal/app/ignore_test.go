package app

import (
	"reflect"
	"testing"
)

func TestApplyIgnoresItem(t *testing.T) {
	got, n := applyIgnores([]string{"a:/x", "btn:Reklam", "h:h1:Ana"}, []ignoreRule{{Kind: "item", A: "btn:Reklam"}})
	if n != 1 || !reflect.DeepEqual(got, []string{"a:/x", "h:h1:Ana"}) {
		t.Fatalf("got %v n=%d", got, n)
	}
}

func TestValueRuleMasksButKeepsPresence(t *testing.T) {
	a, b, err := buildValueRule("a:/oturum/Zk93LmA81xQpWd", "a:/oturum/Zk11QqRt77LmNb")
	if err != nil {
		t.Fatal(err)
	}
	if a != "a:/oturum/" || b != "" {
		t.Fatalf("a=%q b=%q (ortak rastgele karakterler kuralı daraltmamalı)", a, b)
	}
	rules := []ignoreRule{{Kind: "value", A: a, B: b}}
	prev, _ := applyIgnores([]string{"a:/oturum/Zk11QqRt77LmNb", "h:h1:Ana"}, rules)
	cur, _ := applyIgnores([]string{"a:/oturum/Qw8888ppLL0000", "h:h1:Ana"}, rules)
	add, rem := diffTest(prev, cur)
	if len(add)+len(rem) != 0 {
		t.Fatalf("token değişimi fark sayılmamalı: +%v -%v", add, rem)
	}
	// öğe kaybolursa gerçek değişikliktir
	cur, _ = applyIgnores([]string{"h:h1:Ana"}, rules)
	add, rem = diffTest(prev, cur)
	if len(rem) != 1 {
		t.Fatalf("kaybolan öğe değişiklik sayılmalı: +%v -%v", add, rem)
	}
}

func TestValueRuleSingleSideToken(t *testing.T) {
	a, b, err := buildValueRule("in:input:hidden:csrf_9fA3kLm2Pq8XzY1w", "")
	if err != nil {
		t.Fatal(err)
	}
	if a == "" || len(a)+len(b) >= len("in:input:hidden:csrf_9fA3kLm2Pq8XzY1w") {
		t.Fatalf("a=%q b=%q", a, b)
	}
}

func TestValueRuleRejectsTooBroad(t *testing.T) {
	if _, _, err := buildValueRule("btn:Aa", "btn:Bb"); err == nil {
		t.Fatal("çok kısa ortak kısım reddedilmeliydi")
	}
	if _, _, err := buildValueRule("btn:Kaydet", ""); err == nil {
		t.Fatal("token olmayan öğe maskelenmemeli")
	}
}

func diffTest(prev, cur []string) (add, rem []string) {
	p, c := map[string]bool{}, map[string]bool{}
	for _, x := range prev {
		p[x] = true
	}
	for _, x := range cur {
		c[x] = true
		if !p[x] {
			add = append(add, x)
		}
	}
	for _, x := range prev {
		if !c[x] {
			rem = append(rem, x)
		}
	}
	return
}
