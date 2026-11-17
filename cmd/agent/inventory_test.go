package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeOf(t *testing.T) {
	for in, want := range map[string]string{"node": "node", "php-fpm8.2": "php", "python3.11": "python", "nginx": "nginx", "apache2": "httpd", "myapp": ""} {
		if got := runtimeOf(in); got != want {
			t.Errorf("runtimeOf(%q)=%q want %q", in, got, want)
		}
	}
}

func TestParsers(t *testing.T) {
	lock := `{"lockfileVersion":3,"packages":{"":{"name":"app"},"node_modules/lodash":{"version":"4.17.20"},"node_modules/a/node_modules/b":{"version":"1.0.0"}}}`
	ds := parsePackageLock([]byte(lock), "app")
	if len(ds) != 2 {
		t.Fatalf("lock v3: %+v", ds)
	}
	v1 := `{"dependencies":{"express":{"version":"4.17.1","dependencies":{"qs":{"version":"6.7.0"}}}}}`
	if ds := parsePackageLock([]byte(v1), "app"); len(ds) != 2 {
		t.Fatalf("lock v1: %+v", ds)
	}
	if ds := parsePackageJSON([]byte(`{"dependencies":{"a":"^1.2.3","b":"latest","c":"~2.0.0"}}`), "x"); len(ds) != 2 {
		t.Fatalf("package.json: %+v", ds)
	}
	if ds := parseComposerLock([]byte(`{"packages":[{"name":"monolog/monolog","version":"v2.9.1"}]}`), "x"); len(ds) != 1 || ds[0].Version != "2.9.1" {
		t.Fatalf("composer: %+v", ds)
	}
	if ds := parseRequirements([]byte("# c\nDjango==3.2.1\nrequests[security]==2.25.0\nflask>=1\n-r other.txt\n"), "x"); len(ds) != 2 || ds[0].Name != "django" {
		t.Fatalf("requirements: %+v", ds)
	}
	if ds := parsePoetryLock([]byte("[[package]]\nname = \"Jinja2\"\nversion = \"3.0.1\"\n\n[[package]]\nname = \"x\"\nversion = \"1\"\n"), "x"); len(ds) != 2 {
		t.Fatalf("poetry: %+v", ds)
	}
	if ds := parseGoMod([]byte("module m\nrequire (\n\tgithub.com/a/b v1.2.3 // indirect\n\tgolang.org/x/net v0.1.0\n)\nrequire github.com/c/d v2.0.0\n"), "x"); len(ds) != 3 {
		t.Fatalf("go.mod: %+v", ds)
	}
	if ds := parseDepsJSON([]byte(`{"libraries":{"Newtonsoft.Json/12.0.1":{"type":"package"},"app/1.0.0":{"type":"project"}}}`), "x"); len(ds) != 1 || ds[0].Name != "Newtonsoft.Json" {
		t.Fatalf("deps.json: %+v", ds)
	}
}

func TestJarDeps(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "app.jar")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	for _, n := range []string{"BOOT-INF/lib/spring-core-5.3.10.jar", "BOOT-INF/lib/jackson-databind-2.13.0.jar", "BOOT-INF/classes/x.class", "BOOT-INF/lib/nover.jar"} {
		w, _ := zw.Create(n)
		w.Write([]byte("x"))
	}
	zw.Close()
	f.Close()
	ds := jarDeps(p, "app")
	if len(ds) != 2 || ds[0].Name != "spring-core" || ds[0].Version != "5.3.10" || ds[0].Eco != "Maven" {
		t.Fatalf("jar: %+v", ds)
	}
}

func TestManifestsUpAndScanRoot(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "site", "app")
	os.MkdirAll(filepath.Join(app, "bin"), 0o755)
	os.MkdirAll(filepath.Join(app, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(app, "package-lock.json"), []byte(`{"packages":{"node_modules/lodash":{"version":"4.17.20"}}}`), 0o644)
	os.WriteFile(filepath.Join(app, "node_modules", "x", "package-lock.json"), []byte(`{"packages":{"node_modules/zzz":{"version":"1.0.0"}}}`), 0o644)
	ds, dir := manifestsUp(filepath.Join(app, "bin"), "app")
	if len(ds) != 1 || dir != app {
		t.Fatalf("manifestsUp: %+v %s", ds, dir)
	}
	if got := scanRoot(root, timeNowPlus()); len(got) != 1 || got[0].Name != "lodash" {
		t.Fatalf("scanRoot (node_modules atlanmalı): %+v", got)
	}
}

func timeNowPlus() time.Time { return time.Now().Add(10 * time.Second) }
