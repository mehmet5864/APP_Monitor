package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Saldırı yüzeyi envanteri (v3.14.0): dinleyen portlar (ağdan erişilebilir mi, yalnızca yerel mi), portu dinleyen süreç,
// çalışma zamanı sürümü (node, java, php, python, nginx, ...) ve uygulama bağımlılıkları (paket adı + sürüm).
// Dosya içeriği gönderilmez: manifestlerden yalnızca paket adı ve sürümü çıkarılır. Yalnızca okuma yapılır; çalıştırılan tek
// şey, tanınan çalışma zamanı ikililerinin sürüm bayrağıdır (--version) ve kısa bir zaman sınırı vardır.

type invListener struct {
	Port      int    `json:"port"`
	Scope     string `json:"scope"` // net: ağdan erişilebilir adreste dinler; local: yalnızca loopback
	Proc      string `json:"proc"`
	Runtime   string `json:"runtime,omitempty"`
	Version   string `json:"version,omitempty"`
	App       string `json:"app,omitempty"`
	Container bool   `json:"container,omitempty"`
}

type invDep struct {
	Eco     string `json:"eco"` // npm | Packagist | PyPI | Maven | Go | NuGet
	Name    string `json:"name"`
	Version string `json:"version"`
	App     string `json:"app"`
	Src     string `json:"src,omitempty"` // lock: kilit dosyasından (kesin); manifest: bildirilen sürüm; jar: jar adından
}

type invReport struct {
	Listeners []invListener `json:"listeners"`
	Deps      []invDep      `json:"deps"`
}

// listenSock dinleyen bir TCP soketidir (işletim sistemi katmanı üretir).
type listenSock struct {
	pid, port int
	scope     string
}

// procDetail bir sürecin envanter için gereken ayrıntısıdır.
type procDetail struct {
	comm      string
	exe       string // /proc/<pid>/exe gibi çalıştırılabilir yol
	exeReal   string // gerçek yol (sürüm bayrağını çalıştırmak için)
	args      []string
	cwd       string
	container bool
}

const (
	maxInvListeners = 200
	maxInvDeps      = 2500
	maxDepsPerDir   = 800
	maxManifestSize = 8 << 20
)

var invEvery = 30 * time.Minute

var runtimeNames = map[string]string{
	"node": "node", "nodejs": "node", "java": "java", "php": "php", "python": "python", "ruby": "ruby", "dotnet": "dotnet",
	"nginx": "nginx", "httpd": "httpd", "apache2": "httpd", "postgres": "postgresql", "postmaster": "postgresql",
	"mysqld": "mysql", "mariadbd": "mariadb", "redis-server": "redis", "mongod": "mongodb", "sshd": "openssh",
}

// runtimeOf süreç adından çalışma zamanı/servis türünü bulur (php-fpm8.2, python3.11 gibi sürümlü adlar dahil).
func runtimeOf(comm string) string {
	c := strings.ToLower(comm)
	if r, ok := runtimeNames[c]; ok {
		return r
	}
	switch {
	case strings.HasPrefix(c, "php-fpm") || strings.HasPrefix(c, "php"):
		return "php"
	case strings.HasPrefix(c, "python"):
		return "python"
	}
	return ""
}

type verCmd struct {
	args []string
	re   *regexp.Regexp
	bin  string // boş değilse exe yerine bu ikili (PATH'ten) çalıştırılır
}

var versionCmds = map[string]verCmd{
	"node":       {args: []string{"--version"}, re: regexp.MustCompile(`v?(\d+\.\d+\.\d+)`)},
	"java":       {args: []string{"-version"}, re: regexp.MustCompile(`version "?(\d+(?:\.\d+)*(?:_\d+)?)`)},
	"php":        {args: []string{"-v"}, re: regexp.MustCompile(`PHP (\d+\.\d+\.\d+)`)},
	"python":     {args: []string{"--version"}, re: regexp.MustCompile(`Python (\d+\.\d+\.\d+)`)},
	"ruby":       {args: []string{"--version"}, re: regexp.MustCompile(`ruby (\d+\.\d+\.\d+)`)},
	"dotnet":     {args: []string{"--version"}, re: regexp.MustCompile(`(\d+\.\d+\.\d+)`)},
	"nginx":      {args: []string{"-v"}, re: regexp.MustCompile(`nginx/(\d+\.\d+\.\d+)`)},
	"httpd":      {args: []string{"-v"}, re: regexp.MustCompile(`Apache/(\d+\.\d+\.\d+)`)},
	"postgresql": {args: []string{"--version"}, re: regexp.MustCompile(`(\d+(?:\.\d+)+)`)},
	"mysql":      {args: []string{"--version"}, re: regexp.MustCompile(`Ver (\d+\.\d+\.\d+)`)},
	"mariadb":    {args: []string{"--version"}, re: regexp.MustCompile(`Ver (\d+\.\d+\.\d+)`)},
	"redis":      {args: []string{"--version"}, re: regexp.MustCompile(`v=(\d+\.\d+\.\d+)`)},
	"mongodb":    {args: []string{"--version"}, re: regexp.MustCompile(`v(\d+\.\d+\.\d+)`)},
	"openssh":    {args: []string{"-V"}, re: regexp.MustCompile(`OpenSSH_(\d+\.\d+(?:p\d+)?)`), bin: "ssh"},
}

var (
	verMu    sync.Mutex
	verCache = map[string]string{}
)

// runtimeVersion tanınan çalışma zamanının sürümünü sürüm bayrağıyla okur (yol+değişiklik zamanına göre önbelleğe alınır).
func runtimeVersion(rt, exe string) string {
	vc, ok := versionCmds[rt]
	if !ok || exe == "" {
		return ""
	}
	bin := exe
	if vc.bin != "" {
		p, err := exec.LookPath(vc.bin)
		if err != nil {
			return ""
		}
		bin = p
	}
	fi, err := os.Stat(bin)
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	key := bin + "|" + fi.ModTime().String()
	verMu.Lock()
	if v, ok := verCache[key]; ok {
		verMu.Unlock()
		return v
	}
	verMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, vc.args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	out, _ := cmd.CombinedOutput() // java/nginx/ssh sürümü stderr'e yazar; çıkış kodu önemsenmez
	if len(out) > 4096 {
		out = out[:4096]
	}
	ver := ""
	if m := vc.re.FindSubmatch(out); m != nil {
		ver = string(m[1])
	}
	verMu.Lock()
	verCache[key] = ver
	verMu.Unlock()
	return ver
}

// ---- Bağımlılık manifestleri ----

var depNameVerRe = regexp.MustCompile(`^(.+?)-(\d[\w.+~-]*)$`)

func readCapped(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() || fi.Size() > maxManifestSize {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(f, maxManifestSize))
	return b
}

func cleanVer(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimLeft(v, "=v^~<> ")
	if i := strings.IndexAny(v, " ,|"); i > 0 {
		v = v[:i]
	}
	return v
}

func parsePackageLock(b []byte, app string) []invDep {
	var l struct {
		Packages     map[string]struct{ Version string } `json:"packages"`
		Dependencies map[string]json.RawMessage          `json:"dependencies"`
	}
	if json.Unmarshal(b, &l) != nil {
		return nil
	}
	var out []invDep
	seen := map[string]bool{}
	add := func(name, ver string) {
		k := name + "@" + ver
		if name == "" || ver == "" || seen[k] {
			return
		}
		seen[k] = true
		out = append(out, invDep{Eco: "npm", Name: name, Version: ver, App: app, Src: "lock"})
	}
	for k, p := range l.Packages {
		if k == "" {
			continue
		}
		if i := strings.LastIndex(k, "node_modules/"); i >= 0 {
			add(k[i+len("node_modules/"):], p.Version)
		}
	}
	var walk func(m map[string]json.RawMessage, depth int)
	walk = func(m map[string]json.RawMessage, depth int) {
		if depth > 6 {
			return
		}
		for name, raw := range m {
			var d struct {
				Version      string                     `json:"version"`
				Dependencies map[string]json.RawMessage `json:"dependencies"`
			}
			if json.Unmarshal(raw, &d) == nil {
				add(name, d.Version)
				if len(d.Dependencies) > 0 {
					walk(d.Dependencies, depth+1)
				}
			}
		}
	}
	if len(l.Packages) == 0 {
		walk(l.Dependencies, 0)
	}
	return out
}

func parsePackageJSON(b []byte, app string) []invDep {
	var p struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	var out []invDep
	for n, v := range p.Dependencies {
		if cv := cleanVer(v); cv != "" && cv[0] >= '0' && cv[0] <= '9' {
			out = append(out, invDep{Eco: "npm", Name: n, Version: cv, App: app, Src: "manifest"})
		}
	}
	return out
}

func parseComposerLock(b []byte, app string) []invDep {
	var l struct {
		Packages []struct{ Name, Version string } `json:"packages"`
	}
	if json.Unmarshal(b, &l) != nil {
		return nil
	}
	var out []invDep
	for _, p := range l.Packages {
		if v := cleanVer(p.Version); p.Name != "" && v != "" {
			out = append(out, invDep{Eco: "Packagist", Name: p.Name, Version: v, App: app, Src: "lock"})
		}
	}
	return out
}

var reqLineRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.\-]*)(?:\[[^\]]*\])?\s*==\s*([A-Za-z0-9_.+!\-]+)`)

func parseRequirements(b []byte, app string) []invDep {
	var out []invDep
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" || strings.HasPrefix(ln, "#") || strings.HasPrefix(ln, "-") {
			continue
		}
		if m := reqLineRe.FindStringSubmatch(ln); m != nil {
			out = append(out, invDep{Eco: "PyPI", Name: strings.ToLower(m[1]), Version: m[2], App: app, Src: "lock"})
		}
	}
	return out
}

func parsePoetryLock(b []byte, app string) []invDep {
	var out []invDep
	name := ""
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		switch {
		case ln == "[[package]]":
			name = ""
		case strings.HasPrefix(ln, "name = "):
			name = strings.Trim(strings.TrimPrefix(ln, "name = "), `"`)
		case strings.HasPrefix(ln, "version = ") && name != "":
			out = append(out, invDep{Eco: "PyPI", Name: strings.ToLower(name), Version: strings.Trim(strings.TrimPrefix(ln, "version = "), `"`), App: app, Src: "lock"})
			name = ""
		}
	}
	return out
}

func parsePipfileLock(b []byte, app string) []invDep {
	var l struct {
		Default map[string]struct{ Version string } `json:"default"`
	}
	if json.Unmarshal(b, &l) != nil {
		return nil
	}
	var out []invDep
	for n, p := range l.Default {
		if v := cleanVer(p.Version); v != "" {
			out = append(out, invDep{Eco: "PyPI", Name: strings.ToLower(n), Version: v, App: app, Src: "lock"})
		}
	}
	return out
}

func parseGoMod(b []byte, app string) []invDep {
	var out []invDep
	in := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if i := strings.Index(ln, "//"); i >= 0 {
			ln = strings.TrimSpace(ln[:i])
		}
		switch {
		case strings.HasPrefix(ln, "require ("):
			in = true
		case ln == ")":
			in = false
		case in || strings.HasPrefix(ln, "require "):
			f := strings.Fields(strings.TrimPrefix(ln, "require "))
			if len(f) >= 2 && strings.HasPrefix(f[1], "v") {
				out = append(out, invDep{Eco: "Go", Name: f[0], Version: strings.TrimPrefix(f[1], "v"), App: app, Src: "lock"})
			}
		}
	}
	return out
}

func parseDepsJSON(b []byte, app string) []invDep {
	var d struct {
		Libraries map[string]struct{ Type string } `json:"libraries"`
	}
	if json.Unmarshal(b, &d) != nil {
		return nil
	}
	var out []invDep
	for k, l := range d.Libraries {
		if l.Type != "package" {
			continue
		}
		if i := strings.LastIndexByte(k, '/'); i > 0 {
			out = append(out, invDep{Eco: "NuGet", Name: k[:i], Version: k[i+1:], App: app, Src: "lock"})
		}
	}
	return out
}

// jarDeps jar/war içindeki gömülü kütüphanelerin (BOOT-INF/lib, WEB-INF/lib) adlarından bağımlılık çıkarır;
// yalnızca zip dizini okunur.
func jarDeps(path, app string) []invDep {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil
	}
	defer zr.Close()
	var out []invDep
	for _, f := range zr.File {
		n := f.Name
		if !strings.HasSuffix(n, ".jar") || !(strings.Contains(n, "BOOT-INF/lib/") || strings.Contains(n, "WEB-INF/lib/")) {
			continue
		}
		if d := jarNameDep(filepath.Base(n), app); d != nil {
			out = append(out, *d)
		}
		if len(out) >= maxDepsPerDir {
			break
		}
	}
	return out
}

func jarNameDep(file, app string) *invDep {
	base := strings.TrimSuffix(file, ".jar")
	if m := depNameVerRe.FindStringSubmatch(base); m != nil {
		return &invDep{Eco: "Maven", Name: m[1], Version: m[2], App: app, Src: "jar"}
	}
	return nil
}

// dirJarDeps bir dizindeki *.jar dosyalarının adlarından bağımlılık çıkarır (Tomcat lib/ gibi).
func dirJarDeps(dir, app string) []invDep {
	ms, _ := filepath.Glob(filepath.Join(dir, "*.jar"))
	var out []invDep
	for _, m := range ms {
		if d := jarNameDep(filepath.Base(m), app); d != nil {
			out = append(out, *d)
		}
		if len(out) >= maxDepsPerDir {
			break
		}
	}
	return out
}

var manifestParsers = map[string]func([]byte, string) []invDep{
	"package-lock.json": parsePackageLock,
	"composer.lock":     parseComposerLock,
	"requirements.txt":  parseRequirements,
	"poetry.lock":       parsePoetryLock,
	"Pipfile.lock":      parsePipfileLock,
	"go.mod":            parseGoMod,
}

// manifestsIn tek dizindeki manifestleri okur. Kilit dosyası varsa aynı ekosistem için package.json'a bakılmaz.
func manifestsIn(dir, app string) []invDep {
	var out []invDep
	haveNPM := false
	for _, name := range []string{"package-lock.json", "composer.lock", "requirements.txt", "poetry.lock", "Pipfile.lock", "go.mod"} {
		b := readCapped(filepath.Join(dir, name))
		if b == nil {
			continue
		}
		ds := manifestParsers[name](b, app)
		if name == "package-lock.json" && len(ds) > 0 {
			haveNPM = true
		}
		out = append(out, ds...)
	}
	if !haveNPM {
		if b := readCapped(filepath.Join(dir, "package.json")); b != nil {
			out = append(out, parsePackageJSON(b, app)...)
		}
	}
	if ms, _ := filepath.Glob(filepath.Join(dir, "*.deps.json")); len(ms) > 0 {
		for _, m := range ms {
			if b := readCapped(m); b != nil {
				out = append(out, parseDepsJSON(b, app)...)
			}
		}
	}
	if len(out) > maxDepsPerDir {
		out = out[:maxDepsPerDir]
	}
	return out
}

func skipDir(path string) bool {
	switch path {
	case "", "/", "/usr", "/etc", "/var", "/bin", "/sbin", "/lib", "/lib64", "/proc", "/sys", "/dev", "/run", "/tmp", "/root", "/home":
		return true
	}
	return false
}

// manifestsUp dizinde, yoksa en çok 3 üst dizinde manifest arar (uygulama kökü betiğin bir üstünde olabilir).
func manifestsUp(dir, app string) ([]invDep, string) {
	d := dir
	for i := 0; i < 4 && !skipDir(d); i++ {
		if ds := manifestsIn(d, app); len(ds) > 0 {
			return ds, d
		}
		d = filepath.Dir(d)
	}
	return nil, ""
}

var scanSkip = map[string]bool{"node_modules": true, "vendor": true, ".git": true, "venv": true, ".venv": true, "site-packages": true, "__pycache__": true, "proc": true, "sys": true}

// scanRoot açıkça verilen kök dizinde (APM_INVENTORY_DIRS) en çok 4 düzey derinlikte manifest arar.
func scanRoot(root string, deadline time.Time) []invDep {
	var out []invDep
	rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
	visited := 0
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || time.Now().After(deadline) || visited > 20000 {
			return filepath.SkipAll
		}
		visited++
		if !d.IsDir() {
			return nil
		}
		if scanSkip[d.Name()] || strings.Count(p, string(filepath.Separator))-rootDepth > 4 {
			return filepath.SkipDir
		}
		app := filepath.Base(p)
		out = append(out, manifestsIn(p, app)...)
		return nil
	})
	return out
}

// goBuildDeps çalışan bir Go ikilisinin yapı bilgisinden (stdlib sürümü ve modüller) sürümü ve bağımlılıkları okur.
func goBuildDeps(exe, app string) (string, []invDep) {
	bi, err := buildinfo.ReadFile(exe)
	if err != nil {
		return "", nil
	}
	if !strings.HasPrefix(bi.GoVersion, "go1") {
		return "", nil
	}
	var out []invDep
	for _, d := range bi.Deps {
		v := d.Version
		if d.Replace != nil && d.Replace.Version != "" {
			v = d.Replace.Version
		}
		out = append(out, invDep{Eco: "Go", Name: d.Path, Version: strings.TrimPrefix(v, "v"), App: app, Src: "lock"})
		if len(out) >= maxDepsPerDir {
			break
		}
	}
	return strings.TrimPrefix(bi.GoVersion, "go"), out
}

func firstScriptArg(args []string) string {
	for i := 1; i < len(args); i++ {
		a := args[i]
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}

func javaTargets(args []string) (jar string, catalina string) {
	for i, a := range args {
		if a == "-jar" && i+1 < len(args) {
			jar = args[i+1]
		}
		if strings.HasPrefix(a, "-Dcatalina.base=") {
			catalina = strings.TrimPrefix(a, "-Dcatalina.base=")
		}
		if catalina == "" && strings.HasPrefix(a, "-Dcatalina.home=") {
			catalina = strings.TrimPrefix(a, "-Dcatalina.home=")
		}
	}
	return
}

func absPath(p, cwd string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	if cwd == "" {
		return ""
	}
	return filepath.Join(cwd, p)
}

// collectInventory envanteri toplar; dinleyen soket bilgisi alınamazsa (yetki yok / desteklenmeyen sistem) nil döner.
func collectInventory() *invReport {
	socks := listenSockets()
	if socks == nil {
		return nil
	}
	rep := &invReport{Listeners: []invListener{}, Deps: []invDep{}}
	seenL := map[string]bool{}
	seenDep := map[string]bool{}
	doneApp := map[string]bool{}
	addDeps := func(ds []invDep) {
		for _, d := range ds {
			k := d.App + "|" + d.Eco + "|" + d.Name + "|" + d.Version
			if seenDep[k] || len(rep.Deps) >= maxInvDeps {
				continue
			}
			seenDep[k] = true
			rep.Deps = append(rep.Deps, d)
		}
	}
	deadline := time.Now().Add(20 * time.Second)
	for _, s := range socks {
		if len(rep.Listeners) >= maxInvListeners {
			break
		}
		pd := procInfoFor(s.pid)
		rt := runtimeOf(pd.comm)
		l := invListener{Port: s.port, Scope: s.scope, Proc: trunc(pd.comm, 40), Runtime: rt, Container: pd.container}
		app := ""
		var ds []invDep
		if rt != "" && !pd.container {
			l.Version = runtimeVersion(rt, pd.exeReal)
		}
		switch rt {
		case "node", "python", "ruby", "php", "dotnet":
			if sc := absPath(firstScriptArg(pd.args), pd.cwd); sc != "" {
				app = filepath.Base(filepath.Dir(sc))
				ds, _ = manifestsUp(filepath.Dir(sc), app)
			}
			if len(ds) == 0 && pd.cwd != "" {
				app = filepath.Base(pd.cwd)
				ds, _ = manifestsUp(pd.cwd, app)
			}
		case "java":
			jar, cat := javaTargets(pd.args)
			if jar != "" {
				jp := absPath(jar, pd.cwd)
				app = strings.TrimSuffix(filepath.Base(jp), ".jar")
				if !doneApp["jar:"+jp] {
					doneApp["jar:"+jp] = true
					ds = jarDeps(jp, app)
				}
			}
			if cat != "" {
				app = "tomcat"
				if !doneApp["cat:"+cat] {
					doneApp["cat:"+cat] = true
					ds = append(ds, dirJarDeps(filepath.Join(cat, "lib"), app)...)
					wars, _ := filepath.Glob(filepath.Join(cat, "webapps", "*", "WEB-INF", "lib"))
					for _, w := range wars {
						ds = append(ds, dirJarDeps(w, filepath.Base(filepath.Dir(filepath.Dir(w))))...)
					}
				}
			}
		case "":
			// Bilinmeyen ikili: Go yapı bilgisi varsa sürümü ve modülleri oku (kapsayıcıda da /proc/<pid>/exe okunabilir).
			if pd.exe != "" {
				app = trunc(pd.comm, 40)
				if v, gd := goBuildDeps(pd.exe, app); v != "" {
					l.Runtime, l.Version = "go", v
					if !doneApp["go:"+app] {
						doneApp["go:"+app] = true
						ds = gd
					}
				}
			}
		}
		l.App = app
		key := l.Scope + "|" + l.Proc + "|" + strconv.Itoa(l.Port)
		if !seenL[key] {
			seenL[key] = true
			rep.Listeners = append(rep.Listeners, l)
		}
		if app != "" && len(ds) > 0 {
			if !doneApp["m:"+app] || rt == "java" {
				doneApp["m:"+app] = true
				addDeps(ds)
			}
		}
	}
	if dirs := strings.TrimSpace(os.Getenv("APM_INVENTORY_DIRS")); dirs != "" {
		for _, d := range strings.Split(dirs, ",") {
			if d = strings.TrimSpace(d); d != "" {
				addDeps(scanRoot(d, deadline))
			}
		}
	}
	sort.Slice(rep.Listeners, func(i, j int) bool { return rep.Listeners[i].Port < rep.Listeners[j].Port })
	return rep
}
