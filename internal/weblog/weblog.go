// Package weblog sunucu ve ajanın ortak erişim günlüğü kurallarını tutar: gecikme kovaları ve yol normalleştirme.
package weblog

import (
	"regexp"
	"strings"
)

// BucketBounds gecikme kovalarının üst sınırlarıdır (ms); 12. kova sonsuzdur.
var BucketBounds = []float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000}

// NBuckets kova sayısıdır (BucketBounds + sonsuz kova).
const NBuckets = 12

// BucketIndex bir gecikmenin (ms) kova numarasını verir.
func BucketIndex(ms float64) int {
	for i, b := range BucketBounds {
		if ms <= b {
			return i
		}
	}
	return NBuckets - 1
}

var (
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	hexRe  = regexp.MustCompile(`^[0-9a-fA-F]{16,}$`)
)

func digitCount(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n++
		}
	}
	return n
}

// NormalizePath sorgu dizesini atar ve kimlik benzeri parçaları {id}, {uuid}, {token} yapar; böylece
// /siparis/123 ile /siparis/456 aynı uç nokta sayılır ve kişisel veri saklanmaz.
func NormalizePath(uri string) string {
	if i := strings.IndexAny(uri, "?#"); i >= 0 {
		uri = uri[:i]
	}
	if uri == "" {
		return "/"
	}
	if !strings.HasPrefix(uri, "/") {
		uri = "/" + uri
	}
	segs := strings.Split(uri[1:], "/")
	if len(segs) > 8 {
		segs = segs[:8]
	}
	for i, sg := range segs {
		switch {
		case sg == "":
		case strings.Trim(sg, "0123456789") == "":
			segs[i] = "{id}"
		case uuidRe.MatchString(sg):
			segs[i] = "{uuid}"
		case hexRe.MatchString(sg):
			segs[i] = "{token}"
		case strings.Contains(sg, "@"):
			segs[i] = "{email}"
		case len(sg) >= 20:
			segs[i] = "{token}"
		case len(sg) >= 10 && digitCount(sg) >= 3:
			segs[i] = "{id}"
		}
	}
	p := "/" + strings.Join(segs, "/")
	if len(p) > 150 {
		p = p[:150]
	}
	return p
}
