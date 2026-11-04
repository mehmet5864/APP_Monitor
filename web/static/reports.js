// Ayarlar > Raporlar: ön tanımlı, önizlemeli ve yazdırılabilir (PDF) raporlar. Tüm veri mevcut API uçlarından gelir.
(() => {
  'use strict';
  const A = () => window.APM;
  const $ = (sel, root = document) => root.querySelector(sel);

  async function load() {
    const { api } = A();
    const safe = (p, d) => api(p).catch(() => d);
    const [ov, dash, assets, open, resolved, probes, scens, audit, topo, stats, keys, lic, settings] = await Promise.all([
      safe('/api/overview', {}), safe('/api/dashboard', {}), safe('/api/assets', []), safe('/api/alerts?state=open', []), safe('/api/alerts?state=resolved', []),
      safe('/api/probes', []), safe('/api/scenarios', []), safe('/api/audit', []), safe('/api/topology', { nodes: [], edges: [] }), safe('/api/topology/stats', {}),
      safe('/api/keys', []), safe('/api/license', {}), safe('/api/settings', {})]);
    return { ov, dash, assets, open, resolved, probes, scens, audit, topo, stats, keys, lic, settings, now: new Date() };
  }

  // ---- ortak yapı taşları ----
  const pctTxt = (v) => (v == null ? '—' : (v >= 99.95 ? v.toFixed(1) : v.toFixed(2)) + '%');
  const dt = (d) => d.toLocaleString('tr-TR', { dateStyle: 'long', timeStyle: 'short' });
  const count = (arr, fn) => { const m = {}; for (const x of arr) { const k = fn(x); m[k] = (m[k] || 0) + 1; } return Object.entries(m).sort((a, b) => b[1] - a[1]); };
  const avg = (a) => (a.length ? a.reduce((x, y) => x + y, 0) / a.length : null);
  const kindOf = (k) => (k === 'url' ? 'website' : k);

  function paper(title, subtitle, audience, ...body) {
    const { h } = A();
    return h('article', { class: 'paper' },
      h('header', { class: 'paper-head' }, h('div', null, h('span', { class: 'eyebrow' }, 'APP_MON'), h('h1', null, title), h('p', { class: 'muted' }, subtitle)),
        h('div', { class: 'paper-meta' }, h('span', { class: 'tag' }, audience), h('span', { class: 'small muted' }, dt(new Date())))),
      ...body,
      h('footer', { class: 'paper-foot small muted' }, 'APP_MON · otomatik üretilmiş rapor · veriler rapor oluşturulduğu andaki durumu yansıtır'));
  }
  const sec = (title, ...kids) => A().h('section', { class: 'rsec' }, A().h('h2', null, title), ...kids);
  const cols = (...kids) => A().h('div', { class: 'rcols' }, ...kids);
  const card = (title, ...kids) => A().h('div', { class: 'panel rcard' }, title ? A().h('h3', null, title) : null, ...kids);
  const callout = (tone, title, text) => A().h('div', { class: 'callout ' + tone }, A().h('b', null, title), A().h('span', null, text));
  const bullets = (items) => A().h('ul', { class: 'rbul' }, items.map((t) => A().h('li', null, t)));
  const kpis = (...k) => A().h('div', { class: 'kpis' }, ...k);

  // Halka grafik: parts = [[etiket, değer, sınıf]]
  function donut(parts, centerTop, centerSub) {
    const { s, h } = A();
    const total = parts.reduce((x, p) => x + p[1], 0) || 1, R = 46, C = 2 * Math.PI * R;
    const svg = s('svg', { class: 'donut', viewBox: '0 0 120 120', role: 'img', 'aria-label': parts.map((p) => `${p[0]} ${p[1]}`).join(', ') });
    svg.append(s('circle', { class: 'dn-bg', cx: 60, cy: 60, r: R, 'stroke-width': 16, fill: 'none' }));
    let off = 0;
    for (const [lab, v, cls] of parts) {
      if (!v) continue;
      const len = (v / total) * C;
      svg.append(s('circle', { class: 'dn ' + cls, cx: 60, cy: 60, r: R, 'stroke-width': 16, fill: 'none', 'stroke-dasharray': `${Math.max(0, len - 1.5).toFixed(2)} ${(C - len + 1.5).toFixed(2)}`, 'stroke-dashoffset': (-off).toFixed(2), transform: 'rotate(-90 60 60)' }));
      off += len;
    }
    svg.append(s('text', { class: 'dn-top', x: 60, y: 63, 'text-anchor': 'middle' }, String(centerTop)), s('text', { class: 'dn-sub', x: 60, y: 78, 'text-anchor': 'middle' }, centerSub || ''));
    const legend = h('ul', { class: 'dn-legend' }, parts.map(([lab, v, cls]) => h('li', null, h('i', { class: 'sw ' + cls }), `${lab}: `, h('b', null, v))));
    return h('div', { class: 'donut-wrap' }, svg, legend);
  }
  const bars = (pairs, unit, kindFn) => A().rankBars(pairs.map(([label, value, k]) => ({ label, value, kind: kindFn ? kindFn(k) : undefined, text: unit === '' ? String(value) : undefined })), unit);

  function health(d) {
    const av = d.dash.availability_24h, crit = d.open.filter((a) => a.severity === 'critical').length;
    const down = d.assets.filter((a) => a.status === 'down' || a.status === 'offline').length;
    if (crit > 0 || (av != null && av < 95)) return ['crit', 'Kritik', 'Acil müdahale gerektiren sorunlar var.'];
    if (d.open.length > 0 || down > 0 || (av != null && av < 99.5)) return ['warn', 'Dikkat', 'Bazı bileşenlerde sorun var, izlenmeli.'];
    return ['good', 'Sağlıklı', 'Tüm izlenen bileşenler beklenen durumda.'];
  }
  const sevParts = (list) => [['Kritik', list.filter((a) => a.severity === 'critical').length, 'c-crit'], ['Uyarı', list.filter((a) => a.severity === 'warning').length, 'c-warn'], ['Bilgi', list.filter((a) => a.severity === 'info').length, 'c-info']];
  const statusParts = (assets) => [['Çalışıyor', assets.filter((a) => a.status === 'up').length, 'c-good'], ['Erişilemiyor', assets.filter((a) => a.status === 'down').length, 'c-crit'], ['Çevrimdışı', assets.filter((a) => a.status === 'offline').length, 'c-mute'], ['Bilinmiyor', assets.filter((a) => a.status === 'unknown').length, 'c-info']];
  const mttr = (res) => { const m = res.filter((a) => a.resolved_at).map((a) => (new Date(a.resolved_at) - new Date(a.opened_at)) / 60000); return avg(m); };
  const durTxt = (m) => (m == null ? '—' : m < 90 ? Math.round(m) + ' dk' : (m / 60).toFixed(1) + ' sa');
  function assetTable(d, rows) {
    const { h, table, pill, STATUS, KIND, ago } = A();
    return table(['Asset', 'Tür', 'Durum', '24 sa erişilebilirlik', 'Gecikme', 'Son veri'], rows.map((a) => {
      const st = d.stats[a.id] || {};
      return h('tr', null, h('td', null, h('b', null, a.name)), h('td', null, KIND[a.kind] || a.kind), h('td', null, pill(a.status, STATUS[a.status])),
        h('td', { class: 'mono' }, pctTxt(st.avail_24h)), h('td', { class: 'mono' }, st.latency_ms != null ? A().fmt(st.latency_ms) + ' ms' : '—'), h('td', { class: 'mono small' }, ago(a.last_seen)));
    }), 'Asset yok.');
  }

  // ---- raporlar ----
  const REPORTS = [
    { id: 'exec', title: 'Yönetici özeti (Executive)', audience: 'Yönetici', icon: '◆', desc: 'Tek sayfada genel sağlık durumu, erişilebilirlik, açık riskler, öne çıkanlar ve önerilen aksiyonlar. Üst yönetime sunum için.',
      build(d) {
        const { h, kpi, availChart, alertsChart } = A();
        const [tone, label, text] = health(d), av = d.dash.availability_24h;
        const up = d.assets.filter((a) => a.status === 'up').length, total = d.assets.length;
        const okScen = d.scens.filter((s) => s.last_ok === true).length;
        const worst = d.assets.filter((a) => a.status !== 'up').slice(0, 5);
        const crit = d.open.filter((a) => a.severity === 'critical');
        const finds = [`Son 24 saatte ortalama erişilebilirlik ${pctTxt(av)}.`, `${total} izlenen varlığın ${up}'i çalışıyor${total - up ? `, ${total - up}'i sorunlu` : ''}.`,
          d.open.length ? `${d.open.length} açık alarm var (${crit.length} kritik).` : 'Açık alarm bulunmuyor.',
          d.scens.length ? `${d.scens.length} kullanıcı senaryosunun ${okScen}'i son çalıştırmada başarılı.` : 'Tanımlı kullanıcı senaryosu yok.',
          d.dash.avg_latency_ms != null ? `Ortalama yanıt süresi ${A().fmt(d.dash.avg_latency_ms)} ms.` : null,
          d.topo.root_causes && d.topo.root_causes.length ? `Topolojiye göre ${d.topo.root_causes.length} olası kök neden saptandı.` : null].filter(Boolean);
        const actions = [];
        if (crit.length) actions.push('Kritik alarmları öncelikli olarak inceleyin: ' + [...new Set(crit.map((a) => a.asset))].slice(0, 4).join(', ') + '.');
        if (worst.length) actions.push('Sorunlu varlıklar için sahip ekipleri bilgilendirin: ' + worst.map((a) => a.name).join(', ') + '.');
        if (d.lic.asset_limit && d.lic.assets_used / d.lic.asset_limit >= 0.8) actions.push(`Lisans kullanımı %${Math.round(d.lic.assets_used / d.lic.asset_limit * 100)}; kapasite artışı planlayın.`);
        if (d.scens.some((s) => s.auth && s.auth.blocked)) actions.push('Giriş denemesi durdurulmuş senaryolar var; kimlik bilgilerini güncelleyin.');
        if (!actions.length) actions.push('Acil aksiyon gerekmiyor; mevcut izleme ve alarm kurallarını sürdürün.');
        return paper('Yönetici özeti', 'Uygulama izleme ve performans durumu', 'Yönetici',
          h('div', { class: 'rag ' + tone }, h('span', { class: 'rag-dot' }), h('div', null, h('b', null, 'Genel durum: ' + label), h('div', null, text))),
          kpis(kpi('Erişilebilirlik (24 sa)', pctTxt(av), 'probe ve senaryo ortalaması', null, av != null && av < 99 ? 'bad' : ''), kpi('Çalışan varlık', `${up} / ${total}`, 'izlenen toplam', null, up < total ? 'bad' : ''),
            kpi('Açık alarm', String(d.open.length), `${crit.length} kritik`, null, crit.length ? 'bad' : ''), kpi('Ort. yanıt süresi', d.dash.avg_latency_ms != null ? A().fmt(d.dash.avg_latency_ms) + ' ms' : '—', 'probe gecikmesi', null, ''),
            kpi('Senaryo başarısı', d.scens.length ? `${okScen} / ${d.scens.length}` : '—', 'son çalıştırma', null, d.scens.length && okScen < d.scens.length ? 'bad' : '')),
          cols(card('Erişilebilirlik (24 saat)', availChart(d.dash.availability || [])), card('Saat başına alarm', alertsChart(d.dash.alerts_critical || new Array(24).fill(0), d.dash.alerts_other || new Array(24).fill(0)))),
          cols(card('Varlık durumu', donut(statusParts(d.assets), total, 'varlık')), card('Açık alarmlar (önem)', donut(sevParts(d.open), d.open.length, 'alarm'))),
          cols(sec('Öne çıkanlar', bullets(finds)), sec('Önerilen aksiyonlar', bullets(actions))),
          worst.length ? sec('Dikkat gerektiren varlıklar', assetTable(d, worst)) : null);
      } },
    { id: 'avail', title: 'Erişilebilirlik ve performans raporu', audience: 'Teknik', icon: '◔', desc: 'Varlık bazında 24 saatlik erişilebilirlik, gecikme sıralaması, en yavaş uç noktalar ve genel trend.',
      build(d) {
        const { h, kpi, availChart } = A();
        const lat = d.assets.map((a) => [a.name, (d.stats[a.id] || {}).latency_ms, a.kind]).filter((x) => x[1] != null).sort((a, b) => b[1] - a[1]).slice(0, 10);
        const av = d.assets.map((a) => [a.name, (d.stats[a.id] || {}).avail_24h, a.kind]).filter((x) => x[1] != null).sort((a, b) => a[1] - b[1]).slice(0, 10);
        const avs = d.assets.map((a) => (d.stats[a.id] || {}).avail_24h).filter((v) => v != null);
        return paper('Erişilebilirlik ve performans raporu', 'Son 24 saat', 'Teknik',
          kpis(kpi('Genel erişilebilirlik', pctTxt(d.dash.availability_24h), '24 saat', null, ''), kpi('Ort. gecikme', d.dash.avg_latency_ms != null ? A().fmt(d.dash.avg_latency_ms) + ' ms' : '—', 'probe', null, ''),
            kpi('En düşük asset', avs.length ? pctTxt(Math.min(...avs)) : '—', 'erişilebilirlik', null, ''), kpi('%99.9 altı', String(avs.filter((v) => v < 99.9).length), 'varlık sayısı', null, avs.some((v) => v < 99.9) ? 'bad' : '')),
          sec('Saatlik erişilebilirlik', card('', availChart(d.dash.availability || []))),
          cols(sec('En düşük erişilebilirlik', av.length ? bars(av.map(([n, v, k]) => [n, Number(v.toFixed(2)), k]), '%', (k) => k) : h('p', { class: 'muted' }, 'Veri yok.')),
            sec('En yavaş yanıt (ms)', lat.length ? bars(lat.map(([n, v, k]) => [n, Number(v.toFixed(1)), k]), ' ms', (k) => k) : h('p', { class: 'muted' }, 'Gecikme verisi yok.'))),
          sec('Varlık ayrıntıları', assetTable(d, d.assets)));
      } },
    { id: 'alerts', title: 'Alarm analizi raporu', audience: 'Teknik', icon: '▲', desc: 'Önem ve kural dağılımı, en çok alarm üreten varlıklar, ortalama çözülme süresi (MTTR) ve son alarmların dökümü.',
      build(d) {
        const { h, kpi, alertsChart, table, pill, SEV, ago, fmt } = A();
        const all = d.open.concat(d.resolved);
        const byRule = count(all, (a) => a.rule).slice(0, 8), byAsset = count(all, (a) => a.asset).slice(0, 8);
        return paper('Alarm analizi raporu', 'Açık ve kapanmış alarmlar', 'Teknik',
          kpis(kpi('Açık alarm', String(d.open.length), `${d.open.filter((a) => a.severity === 'critical').length} kritik`, null, d.open.length ? 'bad' : ''), kpi('Kapanan (kayıt)', String(d.resolved.length), 'son kayıtlar', null, ''),
            kpi('Ort. çözülme süresi', durTxt(mttr(d.resolved)), 'MTTR', null, ''), kpi('Farklı kural', String(new Set(all.map((a) => a.rule)).size), 'tetiklenen', null, '')),
          cols(card('Önem dağılımı (tümü)', donut(sevParts(all), all.length, 'alarm')), card('Saat başına alarm (24 sa)', alertsChart(d.dash.alerts_critical || new Array(24).fill(0), d.dash.alerts_other || new Array(24).fill(0)))),
          cols(sec('En çok tetiklenen kurallar', byRule.length ? bars(byRule, '') : h('p', { class: 'muted' }, 'Alarm yok.')), sec('En çok alarm üreten varlıklar', byAsset.length ? bars(byAsset, '') : h('p', { class: 'muted' }, 'Alarm yok.'))),
          sec('Açık alarmlar', table(['Önem', 'Kural', 'Asset', 'Değer', 'Açılış'], d.open.map((a) => h('tr', null, h('td', null, pill(a.severity, SEV[a.severity])), h('td', null, a.rule), h('td', null, a.asset), h('td', { class: 'mono' }, `${fmt(a.value)} (${a.comparator} ${a.threshold})`), h('td', { class: 'mono small' }, ago(a.opened_at)))), 'Açık alarm yok.')),
          sec('Son kapanan alarmlar', table(['Önem', 'Kural', 'Asset', 'Süre', 'Kapanış'], d.resolved.slice(0, 15).map((a) => h('tr', null, h('td', null, pill(a.severity, SEV[a.severity])), h('td', null, a.rule), h('td', null, a.asset),
            h('td', { class: 'mono' }, durTxt((new Date(a.resolved_at) - new Date(a.opened_at)) / 60000)), h('td', { class: 'mono small' }, ago(a.resolved_at)))), 'Kapanan alarm yok.')));
      } },
    { id: 'inventory', title: 'Envanter ve lisans raporu', audience: 'Yönetici / Teknik', icon: '▦', desc: 'Varlıkların tür, dil, toplama yöntemi ve duruma göre dağılımı; lisans kullanımı ve tam envanter listesi.',
      build(d) {
        const { h, kpi, KIND, LANG } = A();
        const lim = d.lic.asset_limit || 0, used = d.lic.assets_used || d.assets.length, pc = lim ? Math.round(used / lim * 100) : null;
        return paper('Envanter ve lisans raporu', 'İzlenen varlıkların dökümü', 'Yönetici / Teknik',
          kpis(kpi('Toplam varlık', String(d.assets.length), 'envanter', null, ''), kpi('Lisans kullanımı', `${used} / ${lim || '∞'}`, pc != null ? `%${pc} dolu` : 'sınırsız', null, pc >= 90 ? 'bad' : ''), kpi('Agent ile', String(d.assets.filter((a) => a.collection === 'agent').length), 'toplama', null, ''), kpi('Agent\'sız', String(d.assets.filter((a) => a.collection !== 'agent').length), 'toplama', null, '')),
          cols(card('Duruma göre', donut(statusParts(d.assets), d.assets.length, 'varlık')), card('Toplama yöntemi', donut([['Agent', d.assets.filter((a) => a.collection === 'agent').length, 'c-good'], ['Agent\'sız', d.assets.filter((a) => a.collection !== 'agent').length, 'c-info']], d.assets.length, 'varlık'))),
          cols(sec('Türe göre', bars(count(d.assets, (a) => KIND[kindOf(a.kind)] || a.kind), '', null)), sec('Dile göre', bars(count(d.assets, (a) => LANG[a.language] || a.language), '', null))),
          sec('Tam envanter', assetTable(d, d.assets)));
      } },
    { id: 'probes', title: 'Probe ve senaryo sağlığı raporu', audience: 'Teknik', icon: '◎', desc: 'Probe türlerine göre dağılım, hatalı probe\'lar, senaryo başarıları, değişen sayfalar ve giriş durumları.',
      build(d) {
        const { h, kpi, table, pill, ago, fmt } = A();
        const fail = d.probes.filter((p) => p.last_error), okP = d.probes.filter((p) => p.last_run && !p.last_error).length;
        const chg = d.scens.filter((s) => s.last_changed_at && Date.now() - new Date(s.last_changed_at) < 864e5);
        return paper('Probe ve senaryo sağlığı raporu', 'Agent\'sız izleme ve kullanıcı senaryoları', 'Teknik',
          kpis(kpi('Probe', String(d.probes.length), `${okP} başarılı`, null, ''), kpi('Hatalı probe', String(fail.length), 'son çalıştırma', null, fail.length ? 'bad' : ''), kpi('Senaryo', String(d.scens.length), `${d.scens.filter((s) => s.last_ok).length} başarılı`, null, ''), kpi('Sayfa değişti', String(chg.length), 'son 24 saat', null, chg.length ? 'bad' : '')),
          cols(card('Probe türleri', A().rankBars(count(d.probes, (p) => p.type.toUpperCase()).map(([l, v]) => ({ label: l, value: v, text: String(v) })), '')), card('Probe sonuçları', donut([['Başarılı', okP, 'c-good'], ['Hatalı', fail.length, 'c-crit'], ['Bekliyor', d.probes.filter((p) => !p.last_run).length, 'c-mute']], d.probes.length, 'probe'))),
          sec('Hatalı probe\'lar', table(['Asset', 'Tür', 'Hedef', 'Hata'], fail.map((p) => h('tr', null, h('td', null, h('b', null, p.asset)), h('td', null, p.type.toUpperCase()), h('td', { class: 'mono small' }, p.target), h('td', { class: 'small error' }, p.last_error))), 'Hatalı probe yok.')),
          sec('Senaryolar', table(['Senaryo', 'Tür', 'Aralık', 'Son çalışma', 'Sonuç', 'Süre'], d.scens.map((s) => h('tr', null, h('td', null, h('b', null, s.name), h('div', { class: 'muted small' }, s.asset)), h('td', null, s.kind === 'browser' ? 'Tarayıcı' : 'HTTP'), h('td', { class: 'mono' }, s.interval_sec + ' sn'),
            h('td', { class: 'mono small' }, ago(s.last_run)), h('td', null, s.last_ok == null ? pill('unknown', 'Bekliyor') : s.last_ok ? pill('up', 'Başarılı') : h('span', { class: 'error small' }, s.last_error || 'Başarısız')), h('td', { class: 'mono' }, s.last_duration_ms != null ? fmt(s.last_duration_ms) + ' ms' : '—'))), 'Senaryo yok.')),
          chg.length ? sec('Son 24 saatte değişen sayfalar', bullets(chg.map((s) => `${s.name} (${s.asset}) · ${ago(s.last_changed_at)}`))) : null,
          d.scens.some((s) => s.auth) ? sec('Otomatik giriş durumu', table(['Senaryo', 'Kullanıcı', 'Durum'], d.scens.filter((s) => s.auth).map((s) => h('tr', null, h('td', null, s.name), h('td', null, s.auth.username || '—'), h('td', null, s.auth.blocked ? pill('down', 'Durduruldu') : s.auth.login_fails ? pill('warning', `Hata ${s.auth.login_fails}/3`) : pill('up', 'Normal')))), '')) : null);
      } },
    { id: 'topology', title: 'Topoloji ve bağımlılık riski raporu', audience: 'Teknik', icon: '⬡', desc: 'Bileşenler arası bağımlılıklar, olası kök nedenler, en çok bağımlı olunan (tek hata noktası adayı) varlıklar ve dış bağımlılıklar.',
      build(d) {
        const { h, kpi, KIND } = A();
        const nodes = d.topo.nodes || [], edges = d.topo.edges || [];
        const name = (id) => (nodes.find((n) => n.id === id) || {}).name || String(id);
        const indeg = {}; for (const e of edges) if (e.kind !== 'calls') indeg[e.target] = (indeg[e.target] || 0) + 1;
        const spof = Object.entries(indeg).sort((a, b) => b[1] - a[1]).slice(0, 8).map(([id, n]) => [name(Number(id)), n]);
        const ext = nodes.filter((n) => n.kind === 'external' || String(n.id).startsWith('ext:'));
        const roots = (d.topo.root_causes || []).map(name);
        return paper('Topoloji ve bağımlılık riski raporu', 'Bileşenler arası ilişkiler', 'Teknik',
          kpis(kpi('Bileşen', String(nodes.length), `${ext.length} dış`, null, ''), kpi('Bağlantı', String(edges.length), `${edges.filter((e) => e.auto).length} otomatik`, null, ''), kpi('Olası kök neden', String(roots.length), 'sağlıksız', null, roots.length ? 'bad' : ''), kpi('En çok bağımlı', spof.length ? String(spof[0][1]) : '—', spof.length ? spof[0][0] : '', null, '')),
          roots.length ? callout('crit', 'Olası kök nedenler', roots.join(', ') + ' — sağlıksız olup bağımlı olduğu hiçbir bileşen sağlıksız değil.') : callout('good', 'Kök neden yok', 'Bağımlılık zincirinde sağlıksız bileşen saptanmadı.'),
          cols(sec('En çok bağımlı olunan bileşenler (etki alanı)', spof.length ? bars(spof, '') : h('p', { class: 'muted' }, 'Tanımlı bağımlılık yok.')), sec('Türe göre bileşenler', bars(count(nodes, (n) => KIND[kindOf(n.kind)] || n.kind), '', null))),
          sec('Dış bağımlılıklar', ext.length ? bullets(ext.map((n) => n.name)) : h('p', { class: 'muted' }, 'Dış alan adı bağımlılığı yok.')),
          sec('Bağlantılar', A().table(['Kaynak', 'İlişki', 'Hedef'], edges.filter((e) => !e.auto).map((e) => h('tr', null, h('td', null, name(e.source)), h('td', null, e.kind === 'runs_on' ? 'üzerinde çalışır' : 'bağımlıdır'), h('td', null, name(e.target)))), 'Elle tanımlı bağlantı yok.')));
      } },
    { id: 'audit', title: 'Denetim ve güvenlik raporu', audience: 'Yönetici / Teknik', icon: '⚿', desc: 'API anahtarları, yapılan işlemler, kullanıcı etkinliği ve güvenlik yapılandırma özeti (vault, özel ağ engeli).',
      build(d) {
        const { h, kpi, table, ago } = A();
        const acts = count(d.audit, (a) => a.action), actors = count(d.audit, (a) => a.actor);
        const risks = [];
        risks.push(['warn', 'Gizli değer kasası', 'Parolalar ve token\'lar şu an veritabanında düz metin tutuluyor; kasa entegrasyonu önerilir.']);
        if (d.settings.block_private === false) risks.push(['warn', 'Özel ağ engeli kapalı', 'Probe\'lar özel/iç ağ adreslerine erişebilir (on-premise için normal; SaaS\'ta açık olmalı).']);
        if (d.keys.filter((k) => k.role === 'admin').length > 2) risks.push(['warn', 'Çok sayıda yönetici anahtarı', 'Gereksiz yönetici anahtarlarını iptal edin.']);
        return paper('Denetim ve güvenlik raporu', 'İşlem kayıtları ve yapılandırma', 'Yönetici / Teknik',
          kpis(kpi('Denetim kaydı', String(d.audit.length), 'son kayıtlar', null, ''), kpi('API anahtarı', String(d.keys.length), `${d.keys.filter((k) => k.role === 'admin').length} yönetici`, null, ''), kpi('Farklı işlem', String(acts.length), 'tür', null, ''), kpi('Etkin kullanıcı', String(actors.length), 'kayıtlarda', null, '')),
          sec('Güvenlik değerlendirmesi', ...risks.map(([t, a, b]) => callout(t, a, b))),
          cols(sec('İşlem türleri', acts.length ? bars(acts.slice(0, 8), '') : h('p', { class: 'muted' }, 'Kayıt yok.')), sec('Kullanıcıya göre', actors.length ? bars(actors.slice(0, 8), '') : h('p', { class: 'muted' }, 'Kayıt yok.'))),
          sec('API anahtarları', table(['Etiket', 'Rol', 'Oluşturma'], d.keys.map((k) => h('tr', null, h('td', null, k.label), h('td', null, k.role), h('td', { class: 'mono small' }, ago(k.created_at)))), 'Anahtar yok.')),
          sec('Son işlemler', table(['Zaman', 'Kullanıcı', 'İşlem', 'Ayrıntı'], d.audit.slice(0, 20).map((a) => h('tr', null, h('td', { class: 'mono small' }, ago(a.at)), h('td', null, a.actor), h('td', { class: 'mono' }, a.action), h('td', { class: 'small' }, a.detail))), 'Kayıt yok.')));
      } },
  ];

  async function preview(r) {
    const { h, clearMarks } = A();
    const modal = $('#report-modal'), body = $('#report-body');
    clearMarks();
    $('#report-title').textContent = r.title;
    body.replaceChildren(h('p', { class: 'muted pad' }, 'Rapor hazırlanıyor…'));
    modal.hidden = false; document.body.classList.add('report-open');
    try { body.replaceChildren(r.build(await load())); body.scrollTop = 0; }
    catch (e) { body.replaceChildren(h('p', { class: 'error pad' }, 'Rapor oluşturulamadı: ' + e.message)); }
  }
  function closePreview() { $('#report-modal').hidden = true; document.body.classList.remove('report-open'); A().clearMarks(); }
  document.addEventListener('DOMContentLoaded', () => {
    $('#report-close').addEventListener('click', closePreview);
    $('#report-print').addEventListener('click', () => { A().clearMarks(); window.print(); });
    $('#report-find').addEventListener('click', () => A().toggleFind(true));
    document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && !$('#report-modal').hidden && $('#findbar').hidden) closePreview(); });
  });

  window.APM_REPORTS = { section() {
    const { h } = A();
    return h('section', null, h('h3', null, 'Raporlar'),
      h('p', { class: 'muted' }, 'Ön tanımlı raporlar canlı veriyle oluşturulur. Önizleyin, ardından “Yazdır / PDF” ile PDF olarak kaydedin veya yazdırın.'),
      h('div', { class: 'report-cards' }, REPORTS.map((r) => h('div', { class: 'panel report-card' },
        h('div', { class: 'rc-top' }, h('span', { class: 'rc-icon', 'aria-hidden': 'true' }, r.icon), h('span', { class: 'tag' }, r.audience)),
        h('b', null, r.title), h('p', { class: 'muted small' }, r.desc),
        h('button', { type: 'button', class: 'primary', onclick: () => preview(r) }, 'Önizle')))));
  } };
  const wait = setInterval(() => { if (window.APM) { window.APM.reports = window.APM_REPORTS; clearInterval(wait); } }, 20);
})();
