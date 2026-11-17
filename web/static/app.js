(() => {
  'use strict';

  // ---- Yardımcılar ----
  const $ = (sel, root = document) => root.querySelector(sel);
  const LANG = { none: '—', dotnet: '.NET', java: 'Java', python: 'Python', go: 'Go' };
  const STATUS = { up: 'Çalışıyor', down: 'Erişilemiyor', degraded: 'Kısmen erişilemiyor', offline: 'Çevrimdışı', unknown: 'Bilinmiyor' };
  const KIND = { host: 'Sunucu', service: 'Servis', database: 'Veritabanı', url: 'Web sitesi', website: 'Web sitesi', webservice: 'Web servisi',
    network: 'Ağ', cache: 'Önbellek', queue: 'Kuyruk', external: 'Dış bağımlılık' };
  const KIND_OPTS = ['website', 'webservice', 'service', 'database', 'host', 'network', 'cache', 'queue'].map((k) => [k, KIND[k]]);
  const KIND_AUTO = [['', 'Otomatik']].concat(KIND_OPTS);
  const SEV = { critical: 'Kritik', warning: 'Uyarı', info: 'Bilgi' };

  // Tüm DOM, metin düğümleriyle kurulur; sunucudan gelen değerler hiçbir zaman HTML olarak yorumlanmaz.
  function h(tag, attrs, ...kids) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v == null || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
      else el.setAttribute(k, v === true ? '' : v);
    }
    for (const kid of kids.flat()) {
      if (kid == null || kid === false) continue;
      el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
    }
    return el;
  }

  function ago(iso) {
    if (!iso) return '—';
    const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
    if (s < 60) return s + ' sn önce';
    if (s < 3600) return Math.floor(s / 60) + ' dk önce';
    if (s < 86400) return Math.floor(s / 3600) + ' sa önce';
    return Math.floor(s / 86400) + ' gün önce';
  }
  const fmt = (v) => (v == null ? '—' : Math.abs(v) >= 100 ? v.toFixed(0) : v.toFixed(1));
  const pill = (cls, text) => h('span', { class: 'pill ' + cls }, text);

  let key = '';
  try { key = localStorage.getItem('apm_key') || ''; } catch (e) { /* depolama kapalı olabilir */ }

  async function api(path, opts = {}) {
    const res = await fetch(path, {
      method: opts.method || 'GET',
      headers: { 'X-API-Key': key, 'Content-Type': 'application/json' },
      body: opts.body ? JSON.stringify(opts.body) : undefined,
    });
    if (res.status === 401) { logout(); throw new Error('Oturum geçersiz'); }
    if (res.status === 204) return null;
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || 'HTTP ' + res.status);
    return data;
  }

  // table: pageSize verilirse sayfalama çubuğu hem üste hem alta eklenir; üstte ayrıca "bu listede ara" süzgeci bulunur
  // (tüm satırlarda, yalnızca görünen sayfada değil). Süzgeç ve sayfa, 10 sn'lik otomatik yenilemede korunur.
  // startAt: açılışta görünmesi gereken satırın dizini.
  const TBLSTATE = {};
  function table(headers, rows, emptyText, pageSize, startAt) {
    const tbody = h('tbody');
    const wrap = h('div', { class: 'tablewrap' }, h('table', null,
      h('thead', null, h('tr', null, headers.map((t) => h('th', null, t)))), tbody));
    if (!rows.length) { tbody.append(h('tr', null, h('td', { class: 'empty', colspan: headers.length }, emptyText))); return wrap; }
    if (!pageSize || rows.length < 8) { tbody.append(...rows); return wrap; }
    const st = TBLSTATE[headers.join('|')] || (TBLSTATE[headers.join('|')] = { q: '', page: 0 });
    const hay = (r) => r._hay || (r._hay = (r.dataset.search || r.textContent || '').toLowerCase());
    let list = rows, pages = 1, page = 0;
    const refilter = () => { const q = st.q.trim().toLowerCase(); list = q ? rows.filter((r) => hay(r).includes(q)) : rows; pages = Math.max(1, Math.ceil(list.length / pageSize)); };
    refilter();
    page = startAt > 0 ? Math.min(pages - 1, Math.floor(startAt / pageSize)) : Math.min(pages - 1, st.page || 0);
    const mkBar = (withFilter) => {
      const info = h('span', { class: 'muted small', role: 'status' });
      const btn = (label, aria, fn) => h('button', { type: 'button', class: 'small', 'aria-label': aria, onclick: fn }, label);
      const first = btn('«', 'İlk sayfa', () => go(0)), prev = btn('‹ Önceki', 'Önceki sayfa', () => go(page - 1));
      const next = btn('Sonraki ›', 'Sonraki sayfa', () => go(page + 1)), last = btn('»', 'Son sayfa', () => go(pages - 1));
      const nav = h('span', { class: 'row' }, first, prev, next, last);
      let q = null;
      if (withFilter) {
        q = h('input', { type: 'search', class: 'tblq', placeholder: 'Bu listede ara…', 'aria-label': 'Listede ara', value: st.q });
        q.addEventListener('input', () => { st.q = q.value; refilter(); go(0); });
      }
      return { el: h('div', { class: 'row pager' + (withFilter ? ' pager-top' : '') }, q, info, nav), info, first, prev, next, last, nav };
    };
    const bars = [mkBar(true), mkBar(false)];
    const go = (n) => {
      page = Math.max(0, Math.min(pages - 1, n)); st.page = page;
      const part = list.slice(page * pageSize, (page + 1) * pageSize);
      if (part.length) tbody.replaceChildren(...part);
      else tbody.replaceChildren(h('tr', null, h('td', { class: 'empty', colspan: headers.length }, 'Süzgece uyan kayıt yok.')));
      const txt = list.length ? `${page * pageSize + 1}–${Math.min(list.length, (page + 1) * pageSize)} / ${list.length}${list.length !== rows.length ? ' (toplam ' + rows.length + ')' : ''} kayıt${pages > 1 ? ` · sayfa ${page + 1}/${pages}` : ''}` : `0 / ${rows.length} kayıt`;
      for (const b of bars) {
        b.info.textContent = txt; b.nav.hidden = pages <= 1;
        b.first.disabled = b.prev.disabled = page === 0; b.next.disabled = b.last.disabled = page === pages - 1;
      }
      bars[1].el.hidden = pages <= 1;
    };
    go(page);
    return h('div', null, bars[0].el, wrap, bars[1].el);
  }
  const PAGE = 50;

  // Liste filtreleri: durum modül düzeyinde tutulur (10 sn'lik otomatik yenilemede kaybolmaz).
  const FILTERS = { assetler: { q: '', status: '', kind: '', coll: '', group: '' }, probelar: { q: '', type: '', res: '' }, senaryolar: { q: '', kind: '', res: '' } };
  function filterBar(state, specs, tbl, total) {
    const count = h('span', { class: 'muted small fcount', role: 'status' });
    const apply = () => {
      let n = 0;
      for (const tr of tbl.querySelectorAll('tbody tr[data-search]')) {
        let ok = !state.q || tr.dataset.search.includes(state.q.toLowerCase());
        for (const sp of specs) if (ok && sp.type === 'select' && state[sp.key] && tr.dataset[sp.key] !== state[sp.key]) ok = false;
        tr.hidden = !ok; if (ok) n++;
      }
      count.textContent = n === total ? `${total} kayıt` : `${n} / ${total} kayıt`;
    };
    const ctrls = specs.map((sp) => {
      const el = sp.type === 'select' ? select(null, [['', sp.label + ': tümü']].concat(sp.options), state[sp.key]) : h('input', { type: 'search', placeholder: sp.label, value: state[sp.key], 'aria-label': sp.label });
      el.addEventListener('input', () => { state[sp.key] = el.value; apply(); });
      return el;
    });
    const reset = h('button', { type: 'button', onclick: () => { for (const sp of specs) state[sp.key] = ''; ctrls.forEach((c) => { c.value = ''; }); apply(); } }, 'Temizle');
    setTimeout(apply, 0);
    return h('div', { class: 'filters' }, ctrls, reset, count);
  }

  // Arama kutulu seçici (C4): çok sayıda seçenek (500 asset) için yazdıkça süzen liste; azsa düz select.
  function combo(id, opts, value) {
    if (opts.length <= 30) return select(id, opts, value);
    const cur = opts.find(([v]) => String(v) === String(value));
    const inp = h('input', { id, type: 'search', list: id + '-dl', placeholder: 'Ad yazarak ara…', autocomplete: 'off', value: cur ? cur[1] + ' (#' + cur[0] + ')' : '' });
    inp.comboValue = () => { const m = /\(#(\d+)\)\s*$/.exec(inp.value); return m ? m[1] : ''; };
    const wrap = h('span', { class: 'combo' }, inp, h('datalist', { id: id + '-dl' }, opts.map(([v, t]) => h('option', { value: t + ' (#' + v + ')' }))));
    wrap.comboValue = inp.comboValue;
    return wrap;
  }
  const comboValue = (el) => (el.comboValue ? el.comboValue() : el.value);

  // Çoklu seçim (C2): satırlara onay kutusu ekler; "görünenleri seç" süzgeçle gizlenmeyen satırları seçer.
  function selectable(rows, ids, label) {
    const sel = new Set(), boxes = [], extra = [];
    const info = h('span', { class: 'small', role: 'status' });
    const upd = () => { info.textContent = sel.size ? sel.size + ' ' + label + ' seçili' : ''; extra.forEach((b) => { b.disabled = !sel.size; }); };
    rows.forEach((tr, i) => {
      const cb = h('input', { type: 'checkbox', 'aria-label': label + ' seç', onclick: (e) => e.stopPropagation() });
      cb.addEventListener('change', () => { if (cb.checked) sel.add(ids[i]); else sel.delete(ids[i]); upd(); });
      boxes.push(cb); tr.prepend(h('td', null, cb));
    });
    const selVisible = h('button', { type: 'button', onclick: () => { rows.forEach((tr, i) => { if (!tr.hidden) { boxes[i].checked = true; sel.add(ids[i]); } }); upd(); } }, 'Görünenleri seç');
    const clear = h('button', { type: 'button', onclick: () => { sel.clear(); boxes.forEach((b) => { b.checked = false; }); upd(); } }, 'Seçimi temizle');
    return { sel, bar: (buttons) => { buttons.forEach((b) => extra.push(b)); upd(); return h('div', { class: 'row' }, selVisible, clear, buttons, info); } };
  }
  // Toplu işlem düğmesi: onay ister (isteğe bağlı), uç noktaya {ids, action, ...} yollar, sayfayı yeniler.
  function bulkBtn(label, url, sel, body, confirmText, cls) {
    return h('button', { type: 'button', class: cls || '', onclick: async (ev) => {
      if (confirmText && !confirm(confirmText.replace('{n}', sel.size))) return;
      ev.target.disabled = true;
      try { const r = await api(url, { method: 'POST', body: Object.assign({ ids: [...sel] }, body) }); alert(r.updated + ' kayıt güncellendi.'); render(); } catch (e) { alert(e.message); ev.target.disabled = false; }
    } }, label);
  }
  // Aralık (ve isteğe bağlı zaman aşımı) değiştirme penceresi.
  function bulkSetButton(url, sel, minSec, withTimeout) {
    return h('button', { type: 'button', onclick: () => {
      const iv = h('input', { type: 'number', min: String(minSec), placeholder: 'sn (boş = değişmez)' });
      const to = h('input', { type: 'number', min: '100', max: '60000', placeholder: 'ms (boş = değişmez)' });
      const m = h('p', { class: 'msg', role: 'status' });
      const close = openModal('Seçilenlerin aralığını değiştir', h('div', { class: 'stack' },
        h('p', { class: 'small muted' }, sel.size + ' kayıt değiştirilecek.'), field('Aralık (en az ' + minSec + ' sn)', iv), withTimeout ? field('Zaman aşımı (ms)', to) : null, m,
        h('button', { type: 'button', class: 'primary', onclick: async () => {
          const body = { ids: [...sel], action: 'set' };
          if (iv.value) body.interval_sec = Number(iv.value);
          if (withTimeout && to.value) body.timeout_ms = Number(to.value);
          try { const r = await api(url, { method: 'POST', body }); m.textContent = r.updated + ' kayıt güncellendi.'; setTimeout(() => { close(); render(); }, 700); } catch (e) { m.textContent = e.message; }
        } }, 'Uygula')), true);
    } }, 'Aralığı değiştir…');
  }
  // CSV ayrıştırıcı: ayraç ; veya , (satırlarda sayılarak seçilir), çift tırnaklı alanları destekler.
  function parseCSV(text) {
    const lines = text.replace(/\r/g, '').split('\n').filter((l) => l.trim() && !l.trim().startsWith('#'));
    if (!lines.length) return [];
    const sep = (lines[0].match(/;/g) || []).length > (lines[0].match(/,/g) || []).length ? ';' : ',';
    return lines.map((l) => { const out = []; let cur = '', q = false;
      for (let i = 0; i < l.length; i++) { const c = l[i];
        if (q) { if (c === '"' && l[i + 1] === '"') { cur += '"'; i++; } else if (c === '"') q = false; else cur += c; }
        else if (c === '"') q = true; else if (c === sep) { out.push(cur.trim()); cur = ''; } else cur += c; }
      out.push(cur.trim()); return out; });
  }
  function probeImportDialog() {
    const ta = h('textarea', { rows: '10', spellcheck: 'false', placeholder: 'asset,type,target,interval_sec,timeout_ms\nweb-01,http,https://web01.firma.local/health,60,5000\nweb-01,tcp,web01.firma.local:443,30,3000\ndb-01,icmp,10.0.0.5,30,2000' });
    const fileIn = h('input', { type: 'file', accept: '.csv,.txt,text/csv', onchange: async (e) => { const f0 = e.target.files[0]; if (f0) ta.value = await f0.text(); } });
    const res = h('div', { class: 'stack' });
    const rowsOf = () => { const t = parseCSV(ta.value); if (t.length && /^(asset|type|target)$/i.test(t[0][0])) t.shift();
      return t.map((c) => ({ asset: c[0] || '', type: c[1] || '', target: c[2] || '', interval_sec: Number(c[3]) || 0, timeout_ms: Number(c[4]) || 0 })); };
    const run = async (dry) => {
      const rows = rowsOf(); if (!rows.length) { res.replaceChildren(h('p', { class: 'error small' }, 'Satır bulunamadı.')); return; }
      res.replaceChildren(h('p', { class: 'muted small' }, 'Çalışıyor…'));
      try { const r = await api('/api/probes/import', { method: 'POST', body: { rows, dry_run: dry } });
        const bad = r.results.filter((x) => !x.ok).slice(0, 25);
        res.replaceChildren(h('p', { class: r.failed ? 'error small' : 'small' }, (dry ? 'Doğrulama: ' : 'Sonuç: ') + r.created + ' satır ' + (dry ? 'geçerli' : 'eklendi') + ', ' + r.failed + ' hatalı.'),
          ...bad.map((x) => h('div', { class: 'mono small error' }, 'Satır ' + x.row + ': ' + x.error)));
        if (!dry && r.created) setTimeout(() => render(), 1500);
      } catch (e) { res.replaceChildren(h('p', { class: 'error small' }, e.message)); }
    };
    openModal('Probe\'ları CSV ile içe aktar', h('div', { class: 'stack' },
      h('p', { class: 'small muted' }, 'Sütunlar: asset, type (http, tcp, icmp, dns, tls, udp, ntp, snmp, postgres…), target, interval_sec, timeout_ms. İlk satır başlık olabilir; ayraç virgül veya noktalı virgül; # ile başlayan satırlar atlanır. En fazla 2000 satır. Yeni asset adları lisanstan asset düşer.'),
      fileIn, ta, h('div', { class: 'row' }, h('button', { type: 'button', onclick: () => run(true) }, 'Önce doğrula'), h('button', { type: 'button', class: 'primary', onclick: () => run(false) }, 'İçe aktar')), res), false, true);
  }

  function field(label, input) { return h('label', null, label, input); }
  function select(id, options, value) {
    const el = h('select', { id }, options.map(([v, t]) => h('option', { value: v }, t)));
    if (value != null) el.value = value;
    return el;
  }


  // "Ad: değer" satırlarını nesneye çevirir.
  function parseHeaders(text) {
    const out = {};
    for (const raw of text.split('\n')) {
      const line = raw.trim();
      if (!line) continue;
      const i = line.indexOf(':');
      if (i < 1) throw new Error('Başlık satırı "Ad: değer" biçiminde olmalı: ' + line.slice(0, 40));
      out[line.slice(0, i).trim()] = line.slice(i + 1).trim();
    }
    return out;
  }
  // "ad = json:yol | header:Ad | regex:desen" satırlarını çıkarım listesine çevirir.
  function parseExtract(text) {
    const out = [];
    for (const raw of text.split('\n')) {
      const line = raw.trim();
      if (!line) continue;
      const m = line.match(/^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(json|header|regex):(.+)$/);
      if (!m) throw new Error('Çıkarım satırı "ad = json:yol", "ad = header:Ad" ya da "ad = regex:desen" biçiminde olmalı: ' + line.slice(0, 40));
      out.push({ var: m[1], from: m[2], path: m[3].trim() });
    }
    return out;
  }
  function parseVars(text) {
    const out = {};
    for (const raw of text.split('\n')) {
      const line = raw.trim();
      if (!line) continue;
      const i = line.indexOf('=');
      if (i < 1) throw new Error('Değişken satırı "ad=değer" biçiminde olmalı: ' + line.slice(0, 40));
      out[line.slice(0, i).trim()] = line.slice(i + 1);
    }
    return out;
  }
  const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'];
  const area = (id, rows, ph) => h('textarea', { id, rows: String(rows), placeholder: ph, spellcheck: 'false' });

  // ---- Üst çubuk ----
  const VIEWS = [
    ['genel', 'Genel bakış'], ['assetler', 'Assetler'], ['topoloji', 'Topoloji'], ['probelar', 'Probe\'lar'], ['senaryolar', 'Senaryolar'], ['performans', 'Performans'], ['trace', 'Dağıtık izleme'], ['yuzey', 'Saldırı yüzeyi'], ['ajanlar', 'Ajanlar'], ['ag', 'Ağ araçları'],
    ['alarmlar', 'Alarmlar'], ['sla', 'SLA'], ['raporlar', 'Raporlar'], ['bildirimler', 'Bildirimler'], ['guncelleme', 'Güncelleme'], ['kiracilar', 'Kiracılar'], ['kullanicilar', 'Kullanıcılar'], ['lisans', 'Lisans ve anahtarlar'], ['kasa', 'Kasa'], ['denetim', 'Denetim'], ['kanit', 'Kanıt zinciri'], ['ayarlar', 'Ayarlar'], ['yardim', 'Yardım'],
  ];

  // ---- Yetki rolleri ----
  const ROLE_LABEL = { admin: 'Yönetici', operator: 'İşletmeci', viewer: 'İzleyici', agent: 'Agent' };
  const ADMIN_ONLY = ['kiracilar', 'kullanicilar', 'lisans', 'kasa', 'denetim', 'kanit', 'bildirimler', 'guncelleme', 'ajanlar'];
  let ME = { label: '', role: 'admin' };
  const canWrite = () => ME.role === 'admin' || ME.role === 'operator';
  async function loadMe() {
    try { ME = await api('/api/me'); } catch (e) { ME = { label: '', role: 'viewer' }; }
    document.body.dataset.role = ME.role;
    const b = $('#role-badge');
    b.textContent = (ME.tenant && !ME.platform ? ME.tenant + ' · ' : '') + (ROLE_LABEL[ME.role] || ME.role) + (ME.label ? ' · ' + ME.label : '');
    b.title = 'Oturum açan anahtar: ' + (ME.label || '-') + ' — rol: ' + (ROLE_LABEL[ME.role] || ME.role);
    b.hidden = false;
  }
  // İzleyici rolünde değiştirme düğmelerini devre dışı bırakır (asıl denetim sunucudadır: 403).
  const KEEP_SEL = '#login, .top-right, #findbar, .filters, #report-modal, .report-cards, .seg, .ctx-menu, .lightbox, .keep';
  const WRITE_TXT = /^(Sil|Ekle|Kaydet|Değiştir|Düzenle|Oluştur|Güncelle|Çalıştır|Şimdi|Kaydı başlat|Kaydı bitir|Bağlantı ekle|Probe ekle|Kural ekle|Anahtar oluştur|Senaryoyu kaydet|Giriş|Girişi|Bilgileri|Adımı|\+ Adım|Gruptaki|Kaydet ve|Yerleşimi sıfırla|Üstlen|Pencere|Kanalı)/;
  function lockCtl(b) { b.disabled = true; b.classList.add('locked'); b.title = 'Bu işlem için yetkiniz yok (izleyici rolü yalnızca görüntüleyebilir).'; }
  function enforceRole(root) {
    if (ME.role !== 'viewer') return;
    for (const b of root.querySelectorAll('button')) {
      if (b.classList.contains('locked') || b.closest(KEEP_SEL)) continue;
      const tx = b.textContent.trim();
      if (b.matches('.primary, .danger, [type=submit]') || WRITE_TXT.test(tx) || (b.closest('td') && /^(Kapat|Aç)$/.test(tx))) lockCtl(b);
    }
  }
  let enfQueued = false;
  new MutationObserver(() => {
    if (ME.role !== 'viewer' || enfQueued) return;
    enfQueued = true; requestAnimationFrame(() => { enfQueued = false; enforceRole($('#view')); });
  }).observe($('#view'), { childList: true, subtree: true });

  async function refreshHeader() {
    try {
      const o = await api('/api/overview');
      const lic = o.license;
      $('#mode').textContent = lic.mode === 'saas' ? 'SaaS' : 'On-premise';
      if (o.version) $('#ver').textContent = o.version;
      window.ASSET_N = lic.assets_used;
      $('#meter-label').textContent = `${lic.assets_used} / ${lic.asset_limit} asset`;
      const pct = lic.asset_limit ? Math.min(100, (lic.assets_used / lic.asset_limit) * 100) : 100;
      const fill = $('#meter-fill');
      fill.style.width = pct + '%';
      fill.className = pct >= 100 ? 'full' : pct >= 80 ? 'high' : '';
      let pb = $('#pause-banner');
      const paused = o.monitoring && o.monitoring.paused;
      if (paused && !pb) { pb = h('div', { id: 'pause-banner', class: 'pause-banner', role: 'status' }); $('#view').before(pb); }
      if (pb) {
        if (paused) pb.replaceChildren(h('b', null, '⏸ İzleme duraklatıldı'), ` — probe, senaryo, Keşfet ve alarm değerlendirmesi çalışmıyor${o.monitoring.by ? ' (' + o.monitoring.by + ', ' + ago(o.monitoring.since) + ')' : ''}. `, h('a', { href: '#genel', onclick: (e) => { e.preventDefault(); goTo('genel'); } }, 'Genel bakışa git'));
        else pb.remove();
      }
      return o;
    } catch (e) { return null; }
  }

  // Üst menü: ilgili sayfalar açılır gruplar altında toplanır (tek sayfalık öğeler doğrudan görünür).
  const NAV_GROUPS = [
    ['genel'],
    ['İzleme', ['assetler', 'topoloji', 'probelar', 'senaryolar', 'performans', 'trace', 'yuzey']],
    ['Alarm ve raporlar', ['alarmlar', 'sla', 'raporlar', 'kanit', 'bildirimler']],
    ['Araçlar', ['ag', 'ajanlar']],
    ['Yönetim', ['kiracilar', 'kullanicilar', 'lisans', 'kasa', 'denetim', 'guncelleme', 'ayarlar']],
    ['yardim'],
  ];
  function closeNavMenus(except) {
    for (const g of document.querySelectorAll('#nav .navg.open')) if (g !== except) { g.classList.remove('open'); g.querySelector('button').setAttribute('aria-expanded', 'false'); }
  }
  function buildNav() {
    const nav = $('#nav');
    const label = Object.fromEntries(VIEWS);
    const ok = (id) => label[id] && (id === 'kiracilar' || id === 'guncelleme' ? !!ME.platform : (ME.role === 'admin' || !ADMIN_ONLY.includes(id)));
    const link = (id) => h('a', { href: '#' + id, 'data-v': id }, label[id]);
    const items = [];
    for (const [first, ids] of NAV_GROUPS) {
      if (!ids) { if (ok(first)) items.push(link(first)); continue; }
      const vis = ids.filter(ok);
      if (!vis.length) continue;
      if (vis.length === 1) { items.push(link(vis[0])); continue; }
      const btn = h('button', { type: 'button', class: 'navb', 'aria-haspopup': 'true', 'aria-expanded': 'false' }, first, h('span', { 'aria-hidden': 'true', class: 'caret' }, ' ▾'));
      const menu = h('div', { class: 'navm', role: 'group', 'aria-label': first }, vis.map(link));
      const g = h('div', { class: 'navg', 'data-ids': vis.join(',') }, btn, menu);
      let pinned = false;
      const open = (v) => { if (!v) pinned = false; closeNavMenus(v ? g : null); g.classList.toggle('open', v); btn.setAttribute('aria-expanded', String(v)); };
      btn.addEventListener('click', () => { if (pinned) open(false); else { open(true); pinned = true; } });
      g.addEventListener('mouseenter', () => { if (matchMedia('(hover: hover)').matches) open(true); });
      g.addEventListener('mouseleave', () => { if (matchMedia('(hover: hover)').matches) open(false); });
      g.addEventListener('keydown', (e) => { if (e.key === 'Escape') { open(false); btn.focus(); } });
      g.addEventListener('focusout', (e) => { if (!g.contains(e.relatedTarget)) open(false); });
      menu.addEventListener('click', () => open(false));
      items.push(g);
    }
    nav.replaceChildren(...items);
  }
  document.addEventListener('click', (e) => { if (!e.target.closest('#nav .navg')) closeNavMenus(); });

  // ---- Görünümler ----
  let selectedAsset = null;
  // Genel bakıştan ilgili yere gitme: hashchange seçimi sıfırladığı için hedef önce "bekleyen" olarak saklanır.
  let pendingAsset = null, focusAlertId = null;
  function goTo(view, opts) {
    if (opts && opts.asset != null) { pendingAsset = opts.asset; Object.assign(FILTERS.assetler, { q: '', status: '', kind: '', coll: '', group: '' }); }
    if (opts && opts.alert != null) focusAlertId = opts.alert;
    if (opts && opts.assetFilter) Object.assign(FILTERS.assetler, { q: '', status: '', kind: '', coll: '', group: '' }, opts.assetFilter);
    if (location.hash.slice(1) === view) render(); else location.hash = view;
  }
  // Öğeyi tıklanabilir/klavye erişilebilir yapar (iç düğmeler ve bağlantılar hariç).
  function clickable(el, fn, label) {
    el.classList.add('go');
    if (el.tagName !== 'TR') el.setAttribute('role', 'link'); // tablo satırında satır rolü korunur
    el.setAttribute('tabindex', '0'); if (label) el.setAttribute('title', label);
    el.addEventListener('click', (e) => { if (e.target.closest('button, a, input, select, summary, details')) return; fn(e); });
    el.addEventListener('keydown', (e) => { if ((e.key === 'Enter' || e.key === ' ') && e.target === el) { e.preventDefault(); fn(e); } });
    return el;
  }

  // ---- Dashboard ----
  let tipEl = null;
  function tip(el, text) {
    el.addEventListener('mouseenter', (e) => showTip(e, text));
    el.addEventListener('mousemove', (e) => moveTip(e));
    el.addEventListener('mouseleave', hideTip);
  }
  function showTip(e, text) {
    if (!tipEl) { tipEl = h('div', { class: 'tip', role: 'tooltip' }); document.body.append(tipEl); }
    tipEl.textContent = text; tipEl.hidden = false; moveTip(e);
  }
  function moveTip(e) {
    if (!tipEl) return;
    const x = Math.min(e.clientX + 14, window.innerWidth - tipEl.offsetWidth - 8), y = e.clientY + 16;
    tipEl.style.left = Math.max(8, x) + 'px'; tipEl.style.top = y + 'px';
  }
  function hideTip() { if (tipEl) tipEl.hidden = true; }

  const hourLabel = (i) => { const d = new Date(Date.now() - (23 - i) * 3600e3); return String(d.getHours()).padStart(2, '0') + ':00'; };

  // Sayıyı 0'dan hedefe sayarak gösterir (hareket azaltma tercihi açıksa doğrudan yazar).
  function countUp(el, text) {
    el.textContent = text;
    const m = /^(\d+(?:\.\d+)?)(.*)$/.exec(text);
    if (!m || matchMedia('(prefers-reduced-motion: reduce)').matches) return;
    const target = parseFloat(m[1]), dec = (m[1].split('.')[1] || '').length, t0 = performance.now();
    const step = (now) => {
      const k = Math.min(1, (now - t0) / 800), e = 1 - Math.pow(1 - k, 3);
      el.textContent = (target * e).toFixed(dec) + m[2];
      if (k < 1 && el.isConnected !== false) requestAnimationFrame(step); else el.textContent = text;
    };
    requestAnimationFrame(step);
  }
  function kpi(label, value, sub, extra, tone, go) {
    const kv = h('span', { class: 'kv' + (tone ? ' ' + tone : '') }); countUp(kv, value);
    const el = h('div', { class: 'panel kpi' }, h('span', { class: 'muted small' }, label),
      kv, h('span', { class: 'muted small' }, sub), extra || null);
    return go ? clickable(el, go, label + ' — ayrıntıya git') : el;
  }

  function miniLine(vals) {
    const pts = vals.map((v, i) => (v == null ? null : [i, v])).filter(Boolean);
    if (pts.length < 2) return null;
    const lo = Math.min(...pts.map((p) => p[1])), hi = Math.max(...pts.map((p) => p[1])), span = hi - lo || 1;
    const d = pts.map((p) => `${(p[0] / 23 * 100).toFixed(1)},${(26 - ((p[1] - lo) / span) * 22).toFixed(1)}`).join(' ');
    return s('svg', { class: 'mini', viewBox: '0 0 100 28', preserveAspectRatio: 'none', 'aria-hidden': 'true' }, s('polyline', { points: d }));
  }

  function availChart(vals) {
    const W = 720, H = 210, L = 42, R = 12, T = 12, B = 28, iw = W - L - R, ih = H - T - B;
    const nums = vals.filter((v) => v != null);
    if (!nums.length) return h('p', { class: 'muted' }, 'Son 24 saatte erişilebilirlik verisi yok. Bir probe veya senaryo tanımlayın.');
    const lo = Math.max(0, Math.min(90, Math.floor((Math.min(...nums) - 5) / 10) * 10));
    const x = (i) => L + (iw * i) / 23, y = (v) => T + ih * (1 - (v - lo) / (100 - lo));
    const svg = s('svg', { class: 'chart', viewBox: `0 0 ${W} ${H}`, role: 'img', 'aria-label': 'Son 24 saatte saatlik erişilebilirlik yüzdesi' });
    for (const t of [lo, Math.round((lo + 100) / 2), 100]) {
      svg.append(s('line', { class: 'grid', x1: L, x2: W - R, y1: y(t), y2: y(t) }), s('text', { class: 'tick', x: L - 8, y: y(t) + 4, 'text-anchor': 'end' }, t + '%'));
    }
    for (const i of [0, 6, 12, 18, 23]) svg.append(s('text', { class: 'tick', x: x(i), y: H - 8, 'text-anchor': i === 0 ? 'start' : i === 23 ? 'end' : 'middle' }, i === 23 ? 'şimdi' : hourLabel(i)));
    let seg = [];
    const flush = () => {
      if (seg.length > 1) {
        const line = seg.map((i, k) => `${k ? 'L' : 'M'}${x(i).toFixed(1)} ${y(vals[i]).toFixed(1)}`).join('');
        svg.append(s('path', { class: 'area', d: `${line}L${x(seg[seg.length - 1]).toFixed(1)} ${y(lo)}L${x(seg[0]).toFixed(1)} ${y(lo)}Z` }), s('path', { class: 'line', d: line, pathLength: '1' }));
      }
      seg = [];
    };
    vals.forEach((v, i) => { if (v == null) flush(); else seg.push(i); });
    flush();
    const xh = s('line', { class: 'xh', y1: T, y2: T + ih, display: 'none' });
    const dot = s('circle', { class: 'hotdot', r: 4.5, display: 'none' });
    svg.append(xh, dot);
    vals.forEach((v, i) => {
      if (v != null) svg.append(s('circle', { class: 'pt', cx: x(i), cy: y(v), r: 2.5 }));
      const hit = s('rect', { class: 'hit', x: x(i) - iw / 46, y: T, width: iw / 23, height: ih });
      hit.addEventListener('mouseenter', (e) => { xh.setAttribute('x1', x(i)); xh.setAttribute('x2', x(i)); xh.removeAttribute('display'); if (v != null) { dot.setAttribute('cx', x(i)); dot.setAttribute('cy', y(v)); dot.removeAttribute('display'); } showTip(e, `${hourLabel(i)} · ${v == null ? 'veri yok' : v.toFixed(2) + '% erişilebilir'}`); });
      hit.addEventListener('mousemove', moveTip);
      hit.addEventListener('mouseleave', () => { xh.setAttribute('display', 'none'); dot.setAttribute('display', 'none'); hideTip(); });
      svg.append(hit);
    });
    return svg;
  }

  function alertsChart(crit, other) {
    const W = 720, H = 190, L = 34, R = 12, T = 12, B = 28, iw = W - L - R, ih = H - T - B;
    const tot = crit.map((c, i) => c + other[i]);
    const max = Math.max(1, ...tot);
    const slot = iw / 24, bw = Math.min(20, slot - 6);
    const y = (v) => T + ih * (1 - v / max);
    const svg = s('svg', { class: 'chart', viewBox: `0 0 ${W} ${H}`, role: 'img', 'aria-label': 'Son 24 saatte saat başına açılan alarmlar' });
    for (const t of [0, max]) svg.append(s('line', { class: 'grid', x1: L, x2: W - R, y1: y(t), y2: y(t) }), s('text', { class: 'tick', x: L - 8, y: y(t) + 4, 'text-anchor': 'end' }, t));
    for (const i of [0, 6, 12, 18, 23]) svg.append(s('text', { class: 'tick', x: L + slot * i + slot / 2, y: H - 8, 'text-anchor': i === 0 ? 'start' : i === 23 ? 'end' : 'middle' }, i === 23 ? 'şimdi' : hourLabel(i)));
    for (let i = 0; i < 24; i++) {
      const cx = L + slot * i + slot / 2;
      let yy = T + ih;
      const parts = [[crit[i], 'bar-crit'], [other[i], 'bar-warn']];
      for (const [n, cls] of parts) {
        if (!n) continue;
        const hh = (n / max) * ih;
        { const bar = s('rect', { class: cls, x: cx - bw / 2, y: yy - hh + 1, width: bw, height: Math.max(1, hh - 2), rx: 2 }); bar.style.setProperty('--d', (i * 25) + 'ms'); svg.append(bar); }
        yy -= hh;
      }
      const hit = s('rect', { class: 'hit', x: L + slot * i, y: T, width: slot, height: ih });
      tip(hit, `${hourLabel(i)} · ${tot[i]} alarm (${crit[i]} kritik, ${other[i]} diğer)`);
      svg.append(hit);
    }
    return svg;
  }

  function strip(runs) {
    const W = 240, H = 30, n = 30, step = W / n;
    const svg = s('svg', { class: 'strip', viewBox: `0 0 ${W} ${H}`, role: 'img', 'aria-label': 'Son çalıştırmalar' });
    const mx = Math.max(1, ...runs.filter((r) => r.ok).map((r) => r.ms));
    runs.forEach((r, i) => {
      const x = W - (runs.length - i) * step + 1;
      const hh = r.ok ? 8 + (r.ms / mx) * (H - 10) : H - 2;
      const rect = s('rect', { class: r.ok ? 'tick-ok' : 'tick-bad', x, y: H - hh, width: step - 2.5, height: hh, rx: 1.5 });
      rect.style.setProperty('--d', (i * 18) + 'ms');
      tip(rect, r.ok ? `Başarılı · ${Math.round(r.ms)} ms` : 'Başarısız');
      svg.append(rect);
    });
    return svg;
  }

  function rankBars(items, unit) {
    const max = Math.max(1, ...items.map((i) => i.value));
    return h('div', { class: 'rank' }, items.map((it, ix) => {
      const fill = h('span', { class: 'fill' }); fill.style.setProperty('--d', (ix * 70) + 'ms'); fill.style.width = Math.max(2, (it.value / max) * 100) + '%';
      const row = h('div', { class: 'rk' }, h('span', { class: 'rk-name kl' }, it.kind ? kindIcon(it.kind, 30) : null, it.label),
        h('span', { class: 'track' }, fill), h('span', { class: 'rk-val mono' }, it.text || (fmt(it.value) + (unit || ''))));
      return it.go ? clickable(row, it.go, it.label + ' — ayrıntıya git') : row;
    }));
  }


  // Halka (pie/donut) grafik: dilimler ve açıklama satırları tıklanabilir, ortada toplam yazar.
  // items: [{label, value, cls ('up','down','c1'...), go?, icon?}]
  function donut(items, o) {
    o = o || {};
    const live = items.filter((i) => i.value > 0), total = live.reduce((a, i) => a + i.value, 0);
    const R = 54, C = 2 * Math.PI * R;
    const svg = s('svg', { class: 'donut', viewBox: '0 0 150 150', role: 'img', 'aria-label': (o.label || 'Dağılım') + ': ' + live.map((i) => `${i.label} ${i.value}`).join(', ') });
    svg.append(s('circle', { class: 'dn-track', cx: 75, cy: 75, r: R }));
    let acc = 0;
    live.forEach((it) => {
      const gap = live.length > 1 ? 2.5 : 0, len = Math.max(0.5, (it.value / total) * C - gap);
      const c = s('circle', { class: 'dn-seg cs-' + it.cls, cx: 75, cy: 75, r: R, 'stroke-dasharray': `${len.toFixed(2)} ${(C - len).toFixed(2)}`, 'stroke-dashoffset': String(-acc), transform: 'rotate(-90 75 75)' });
      acc += (it.value / total) * C;
      tip(c, `${it.label}: ${it.value} (%${Math.round((it.value / total) * 100)})`);
      svg.append(it.go ? clickable(c, it.go, it.label + ' — listele') : c);
      if (it.go) c.classList.add('go');
    });
    const big = s('text', { class: 'dn-big', x: 75, y: 80 }); big.textContent = o.center != null ? String(o.center) : String(total);
    const sub = s('text', { class: 'dn-sub', x: 75, y: 98 }); sub.textContent = o.sub || '';
    svg.append(big, sub);
    const legend = h('div', { class: 'dn-legend' }, items.filter((i) => i.value > 0 || o.showZero).map((it) => {
      const row = h('div', { class: 'dn-row cs-' + it.cls }, h('span', { class: 'dn-dot' }), h('span', { class: 'nm' }, it.icon || null, it.label),
        h('span', { class: 'v' }, String(it.value)), h('span', { class: 'pc' }, total ? '%' + Math.round((it.value / total) * 100) : ''));
      return it.go ? clickable(row, it.go, it.label + ' — listele') : row;
    }));
    if (!live.length) legend.replaceChildren(h('p', { class: 'muted small' }, o.empty || 'Veri yok.'));
    return h('div', { class: 'donut-wrap' }, svg, legend);
  }
  function donutPanel(title, items, o) {
    return h('div', { class: 'panel donut-panel' + (o && o.wide ? ' wide' : '') + (o && o.solo ? ' solo' : '') }, h('h3', null, title), donut(items, o));
  }

  // Kontrol edilen sayfaların küçültülmüş görüntüleri; çerçeve rengi durumu gösterir.
  function showShot(url, title) {
    const close = () => { ov.remove(); document.removeEventListener('keydown', onKey); };
    const onKey = (e) => { if (e.key === 'Escape') close(); };
    const ov = h('div', { class: 'lightbox', role: 'dialog', 'aria-label': title, onclick: close }, h('img', { src: url, alt: title }), h('p', null, title));
    document.addEventListener('keydown', onKey);
    document.body.append(ov);
  }

  // Parmak izi öğesini okunur Türkçe etikete çevirir.
  function chgLabel(x) {
    const i = x.indexOf(':'); const k = x.slice(0, i); const v = x.slice(i + 1);
    switch (k) {
      case 'title': return ['Sayfa başlığı', v];
      case 'h': { const j = v.indexOf(':'); return ['Başlık (' + v.slice(0, j).toUpperCase() + ')', v.slice(j + 1)]; }
      case 'form': { const j = v.indexOf(':'); return ['Form (' + v.slice(0, j).toUpperCase() + ')', v.slice(j + 1) || '/']; }
      case 'in': { const p = v.split(':'); return ['Giriş alanı', (p[2] || '(adsız)') + ' · ' + p[0] + (p[1] ? '/' + p[1] : '')]; }
      case 'btn': return ['Buton', v];
      case 'a': return ['Bağlantı', v || '/'];
      case 'script': return ['Harici script', v];
      default: return ['Öğe', x];
    }
  }

  // ---- Yoksay kuralları: değişiklik alarmından öğe/değer hariç tutma (parola/anahtar doğrulamalı) ----
  const looksLikeToken = (x) => /[A-Za-z0-9_\-+/=.]{20,}/.test(x) && /\d/.test(x) && /[A-Za-z]/.test(x);
  function bestPeer(item, others) {
    let best = '', bl = 4;
    for (const o of others) { let i = 0; while (i < item.length && i < o.length && item[i] === o[i]) i++; if (i > bl) { bl = i; best = o; } }
    return best;
  }
  function ignoreButtons(sc, step, item, others, after) {
    const peer = bestPeer(item, others);
    return h('span', { class: 'row ign-btns' },
      h('span', { class: looksLikeToken(item) ? 'pill-s' : 'pill-s off', title: 'Rastgele token/oturum değerine benziyor' }, 'token?'),
      h('button', { type: 'button', class: 'small', title: 'Öğe sayfada kalsın, yalnızca değeri değişince uyarma (sayaç, token, tarih)',
        onclick: (e) => { e.stopPropagation(); ignoreDialog(sc, step, 'value', item, peer, after); } }, 'Değeri yoksay'),
      h('button', { type: 'button', class: 'small', title: 'Bu öğe tamamen karşılaştırmadan çıkarılır (reklam, banner)',
        onclick: (e) => { e.stopPropagation(); ignoreDialog(sc, step, 'item', item, peer, after); } }, 'Öğeyi yoksay'));
  }
  function ignoreDialog(sc, step, kind, item, other, after) {
    const [lab, val] = chgLabel(item);
    const msg = h('p', { class: 'msg small' });
    const useKey = !ME.account;
    const inp = h('input', { type: 'password', id: 'ign-pw', autocomplete: useKey ? 'off' : 'current-password', placeholder: useKey ? 'API anahtarınızı yeniden girin' : 'Parolanız', required: true });
    const close = () => { ov.remove(); document.removeEventListener('keydown', onKey, true); };
    const onKey = (e) => { if (e.key === 'Escape') { e.stopPropagation(); close(); } };
    const go = h('button', { type: 'submit', class: 'primary' }, 'Onayla ve yoksay');
    const f = h('form', { class: 'stack', onsubmit: async (e) => {
      e.preventDefault(); go.disabled = true; msg.className = 'msg small'; msg.textContent = '';
      try {
        await api('/api/scenarios/' + sc.id + '/ignores', { method: 'POST', body: { step, kind, item, other, password: useKey ? undefined : inp.value, api_key: useKey ? inp.value : undefined } });
        close(); if (after) after(); alert('Kural eklendi. Sonraki kontrolden itibaren geçerli olur; mevcut uyarıyı "Uyarıyı temizle" ile kapatabilirsiniz.'); render();
      } catch (err) { msg.className = 'msg error small'; msg.textContent = err.message; go.disabled = false; inp.select(); }
    } },
      h('p', { class: 'small' }, kind === 'value'
        ? 'Bu öğenin DEĞERİ artık değişiklik alarmına girmeyecek (öğe sayfadan kaybolursa yine uyarılır).'
        : 'Bu öğe artık karşılaştırmaya hiç girmeyecek; eklenmesi veya kaldırılması uyarı üretmez.'),
      h('p', { class: 'small' }, h('b', null, lab + ': '), h('span', { class: 'mono' }, val)),
      h('p', { class: 'small muted' }, 'Bu işlem gerçek bir değişikliği de gizleyebilir. Onaylamak için ' + (useKey ? 'API anahtarınızı' : 'parolanızı') + ' girin.'),
      inp, msg, h('div', { class: 'row' }, h('button', { type: 'button', onclick: close }, 'Vazgeç'), go));
    const ov = h('div', { class: 'modal-ov', role: 'dialog', 'aria-label': 'Yoksay onayı', onclick: close },
      h('div', { class: 'modal', onclick: (e) => e.stopPropagation() }, h('h3', null, kind === 'value' ? 'Değeri yoksay' : 'Öğeyi yoksay'), f));
    ov.firstChild.classList.add('modal-sm');
    document.addEventListener('keydown', onKey, true);
    document.body.append(ov); inp.focus();
  }
  function ignoreList(sc) {
    const box = h('div', { class: 'chg-step' });
    const load = async () => {
      let rules = [];
      try { rules = await api('/api/scenarios/' + sc.id + '/ignores'); } catch (e) { return; }
      if (!rules.length) { box.replaceChildren(); return; }
      box.replaceChildren(h('h4', null, 'Yoksayma kuralları (' + rules.length + ')'),
        h('table', { class: 'chg-tbl' }, rules.map((r) => h('tr', null,
          h('td', { class: 'small muted' }, 'adım ' + (r.step + 1)),
          h('td', { class: 'small' }, r.kind === 'value' ? 'değer' : 'öğe'),
          h('td', { class: 'mono small' }, r.kind === 'value' ? r.a + '~' + (r.b || '') : r.a),
          h('td', { class: 'small muted' }, r.by || ''),
          canWrite() ? h('td', null, h('button', { type: 'button', class: 'small', onclick: async (e) => {
            e.target.disabled = true;
            try { await api('/api/scenarios/' + sc.id + '/ignores/' + r.id, { method: 'DELETE' }); load(); } catch (err) { alert(err.message); e.target.disabled = false; }
          } }, 'Kaldır')) : null))));
    };
    load();
    return box;
  }

  // Kart ayrıntıları: hata sebebi, sayfa değişiklikleri ve son çalıştırma özeti.
  async function showChanges(sc) {
    const close = () => { ov.remove(); document.removeEventListener('keydown', onKey); };
    const onKey = (e) => { if (e.key === 'Escape') close(); };
    const body = h('div', { class: 'modal-body' }, h('p', { class: 'muted' }, 'yükleniyor…'));
    const ov = h('div', { class: 'modal-ov', role: 'dialog', 'aria-label': 'Ayrıntılar', onclick: close },
      h('div', { class: 'modal', onclick: (e) => e.stopPropagation() },
        h('div', { class: 'row' }, h('h3', null, sc.name + ' — ayrıntılar'), h('button', { type: 'button', class: 'small', onclick: close }, 'Kapat')), body));
    document.addEventListener('keydown', onKey);
    document.body.append(ov);
    try {
      const recent = sc.last_changed_at && Date.now() - new Date(sc.last_changed_at) < 864e5;
      const [runs, d] = await Promise.all([
        api(`/api/scenarios/${sc.id}/runs`).catch(() => []),
        recent ? api(`/api/scenarios/${sc.id}/last-change`).catch(() => null) : Promise.resolve(null)]);
      const parts = [h('p', { class: 'small muted' }, sc.asset + ' · son kontrol ' + ago(sc.last_run))];
      const list = Array.isArray(runs) ? runs : (runs.runs || []);
      const last = list[0];
      if (sc.last_ok === false) {
        const steps = ((list.find((r) => !r.ok) || {}).steps || []).filter((x) => !x.ok);
        parts.push(h('div', { class: 'chg-step' }, h('h4', { class: 'error' }, 'Hata sebebi'),
          h('p', { class: 'mono small error' }, sc.last_error || (last && last.error) || 'Hata ayrıntısı kaydedilmemiş.'),
          ...steps.map((st) => h('p', { class: 'small' }, h('b', null, st.name), st.page ? ' · ' + st.page : '', ' — ', h('span', { class: 'error' }, st.error || 'başarısız')))));
      }
      if (d && d.steps && d.steps.length) {
        const sec = (t, cls, sign, items, st, others) => items.length ? h('div', { class: 'chg-sec' }, h('b', { class: cls }, t + ' (' + items.length + ')'),
          h('table', { class: 'chg-tbl' }, items.map((x) => { const [kind, val] = chgLabel(x); return h('tr', { class: cls }, h('td', { class: 'sg' }, sign), h('td', { class: 'small muted' }, kind), h('td', { class: 'mono small' }, val),
            canWrite() ? h('td', { class: 'ign-act' }, ignoreButtons(sc, st.idx, x, others, () => close())) : null); }))) : null;
        parts.push(h('div', { class: 'chg-step' }, h('h3', null, 'Tespit edilen sayfa değişiklikleri'),
          h('p', { class: 'small muted' }, 'Tespit: ' + new Date(d.at).toLocaleString((window.LOCALE || 'tr-TR')) + ' (' + ago(d.at) + '). Karşılaştırma bir önceki çalıştırmayadır.'),
          ...d.steps.map((st) => h('div', { class: 'chg-step' }, h('h4', null, st.name + (st.page ? ' · ' + st.page : '')),
            sec('Eklenen', 'add', '+', st.added || [], st, st.removed || []), sec('Kaldırılan', 'del', '−', st.removed || [], st, st.added || [])))));
        parts.push(ignoreList(sc));
      } else if (recent) {
        parts.push(h('p', { class: 'muted small' }, 'Değişiklik ayrıntı kaydı bulunamadı (eski çalıştırmalar silinmiş olabilir).'));
      }
      if (last) {
        parts.push(h('div', { class: 'chg-step' }, h('h4', null, 'Son çalıştırma · ' + (last.ok ? 'başarılı' : 'başarısız') + ' · ' + fmt(last.duration_ms) + ' ms'),
          ...(last.steps || []).map((st) => h('p', { class: 'small' }, (st.ok ? '✓ ' : '✗ ') + st.name + ' — ' + fmt(st.ms) + ' ms' + (st.error ? ' — ' + st.error : '')))));
      }
      if (parts.length === 1) parts.push(h('p', { class: 'muted' }, 'Gösterilecek ayrıntı yok.'));
      body.replaceChildren(...parts);
    } catch (e) { body.replaceChildren(h('p', { class: 'error' }, e.message)); }
  }

  function changeBox(sc) {
    const ch = sc.change;
    const body = ch
      ? h('ul', { class: 'chg-list' }, ch.items.map((t) => h('li', { class: t.startsWith('+') ? 'add' : 'del' }, t)),
        ch.total > ch.items.length ? h('li', { class: 'muted' }, `… ve ${ch.total - ch.items.length} değişiklik daha`) : null)
      : h('span', { class: 'muted small' }, 'Ayrıntı kaydı bulunamadı (eski çalıştırmalar silinmiş olabilir).');
    const btn = h('button', { type: 'button', class: 'small', onclick: async (e) => {
      e.stopPropagation(); btn.disabled = true;
      try { await api(`/api/scenarios/${sc.id}/ack-changes`, { method: 'POST' }); render(); } catch (err) { btn.disabled = false; alert(err.message); }
    } }, 'Uyarıyı temizle');
    const det = h('button', { type: 'button', class: 'small', onclick: (e) => { e.stopPropagation(); showChanges(sc); } }, 'Ayrıntıları göster');
    return h('div', { class: 'chg-box', onclick: (e) => e.stopPropagation() },
      h('div', { class: 'small' }, h('b', null, 'Neden değişti? '), ch ? h('span', { class: 'muted' }, ago(ch.at)) : null), body, h('div', { class: 'row' }, det, btn));
  }

  // Kartta probe'un hızlı durumunu ve senaryonun son sonucunu yan yana gösterir (renk nedeni belli olsun).
  function probeLine(sc) {
    const p = sc.probe;
    if (!p || !p.enabled) return null;
    const lbl = String(p.type).toUpperCase() + ' ' + p.target;
    let txt, cls = 'muted small';
    if (!p.fresh || p.ok === undefined) txt = 'Probe (' + lbl + '): güncel veri yok';
    else if (p.ok) txt = 'Probe (' + lbl + '): erişilebilir · ' + ago(p.last_run);
    else if (p.fail_streak >= 2) { txt = 'Probe (' + lbl + '): ' + p.fail_streak + ' kez erişilemedi · ' + ago(p.last_run) + (p.maint ? ' · bakım penceresinde' : ''); cls = (p.maint ? 'muted' : 'error') + ' small'; }
    else { txt = 'Probe (' + lbl + '): 1 hata, doğrulanıyor · ' + ago(p.last_run); }
    const sen = sc.last_run ? ' · Senaryo son: ' + ago(sc.last_run) + (sc.last_ok === false ? ' başarısız' : sc.last_ok ? ' başarılı' : '') : '';
    return h('span', { class: cls, title: p.error || '' }, txt + sen);
  }

  function pageFrames(scenarios) {
    const list = scenarios.filter((x) => x.kind === 'browser');
    if (!list.length) return null;
    // Eşleşen probe: ardışık 2 hata (bakımda değilse ve veri tazeyse) kartı senaryoyu beklemeden kırmızı yapar.
    const probeDown = (sc) => { const p = sc.probe; return !!p && p.enabled && p.fresh && !p.maint && p.ok === false && p.fail_streak >= 2; };
    const state = (sc) => {
      if (probeDown(sc)) return ['bad', 'Probe erişemiyor'];
      // Son 3 çalıştırma: hepsi başarısız = kırmızı, en az biri başarısız = sarı, hepsi başarılı = yeşil.
      const l3 = (sc.runs || []).slice(-3);
      if (l3.length && l3.every((r) => !r.ok)) return ['bad', 'Başarısız (son ' + l3.length + ' kontrol)'];
      if (l3.some((r) => !r.ok)) return ['deg', 'Aralıklı hata (' + l3.filter((r) => !r.ok).length + '/' + l3.length + ')'];
      if (!l3.length && sc.last_ok === false) return ['bad', 'Başarısız'];
      if (sc.last_changed_at && Date.now() - new Date(sc.last_changed_at) < 864e5) return ['chg', 'Sayfa değişti'];
      if (sc.last_ok === true) return ['ok', 'Sağlıklı'];
      return ['wait', 'Bekliyor'];
    };
    const order = { ok: 0, chg: 1, wait: 2, deg: 3, bad: 4 };
    list.sort((a, b) => order[state(a)[0]] - order[state(b)[0]] || a.name.localeCompare(b.name));
    // Kullanıcının sürükle-bırak sırası ve kaldırdığı kartlar (tarayıcıda saklanır)
    const lsGet = (k) => { try { const v = JSON.parse(localStorage.getItem(k) || '[]'); return Array.isArray(v) ? v : []; } catch (e) { return []; } };
    const lsSet = (k, v) => { try { localStorage.setItem(k, JSON.stringify(v)); } catch (e) { /* saklama kapalı */ } };
    const savedOrder = lsGet('apm_dash_order');
    if (savedOrder.length) {
      const pos = (x) => { const i = savedOrder.indexOf(x.id); return i < 0 ? 1e6 : i; };
      list.sort((a, b) => pos(a) - pos(b));
    }
    const hiddenIds = lsGet('apm_dash_hidden');
    const hiddenCount = list.filter((x) => hiddenIds.includes(x.id)).length;
    const visible = list.filter((x) => !hiddenIds.includes(x.id));
    const grid = h('div', { class: 'thumbs' });
    const hideCard = (id) => { lsSet('apm_dash_hidden', [...new Set([...lsGet('apm_dash_hidden'), id])]); render(); };
    const menu = (e, sc) => {
      e.preventDefault(); e.stopPropagation();
      document.querySelectorAll('.ctx-menu').forEach((m) => m.remove());
      const m = h('div', { class: 'ctx-menu', role: 'menu' },
        h('button', { type: 'button', role: 'menuitem', onclick: () => { m.remove(); hideCard(sc.id); } }, 'Dashboard\'dan kaldır'));
      m.style.left = Math.min(e.clientX, window.innerWidth - 200) + 'px'; m.style.top = e.clientY + 'px';
      document.body.append(m);
      const close = (ev) => { if (ev.type === 'keydown' && ev.key !== 'Escape') return; m.remove(); document.removeEventListener('click', close); document.removeEventListener('keydown', close); };
      setTimeout(() => { document.addEventListener('click', close); document.addEventListener('keydown', close); }, 0);
    };
    let dragEl = null;
    const cards = visible.map((sc) => {
      const [cls, label] = state(sc);
      const imgBox = h('div', { class: 'thumb-img' }, h('span', { class: 'muted small' }, sc.shot ? 'yükleniyor…' : 'görüntü yok'));
      const card = h('div', { class: 'thumb ' + cls, tabindex: sc.shot ? '0' : null, role: sc.shot ? 'button' : null, 'aria-label': `${sc.name}, ${label}` }, imgBox,
        h('div', { class: 'thumb-meta' }, h('b', null, sc.name), h('span', { class: 'muted small' }, sc.asset),
          h('span', { class: 'row small' }, h('a', { href: '#', class: 'pill-link', title: 'Ayrıntıları göster', onclick: (e) => { e.preventDefault(); e.stopPropagation(); showChanges(sc); } }, pill(cls === 'bad' ? 'down' : cls === 'chg' || cls === 'deg' ? 'warning' : cls === 'ok' ? 'up' : 'unknown', label)),
            h('span', { class: 'mono muted' }, ago(sc.last_run))),
          probeLine(sc),
          sc.last_ok === false && sc.last_error ? h('a', { href: '#', class: 'error small', title: 'Hata ayrıntısı', onclick: (e) => { e.preventDefault(); e.stopPropagation(); showChanges(sc); } }, sc.last_error.slice(0, 90)) : null,
          sc.last_ok === false && sc.shot ? h('span', { class: 'muted small' }, 'Görüntü son başarılı kontrolden: ' + ago(sc.shot.at)) : null,
          cls === 'chg' ? changeBox(sc) : null));
      if (sc.shot) {
        fetch(`/api/scenarios/${sc.id}/runs/${sc.shot.run_id}/shots/${sc.shot.step}`, { headers: { 'X-API-Key': key } })
          .then((r) => (r.ok ? r.blob() : Promise.reject(new Error('görüntü yok'))))
          .then((b) => {
            const url = URL.createObjectURL(b);
            imgBox.replaceChildren(h('img', { src: url, alt: sc.name + ' sayfa görüntüsü', loading: 'lazy' }));
            const open = () => showShot(url, `${sc.name} · ${label} · ${ago(sc.shot.at)}`);
            card.addEventListener('click', open);
            card.addEventListener('keydown', (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(); } });
          }).catch(() => imgBox.replaceChildren(h('span', { class: 'muted small' }, 'görüntü yüklenemedi')));
      }
      card.dataset.id = sc.id; card.draggable = true;
      card.addEventListener('contextmenu', (e) => menu(e, sc));
      card.addEventListener('dragstart', (e) => { dragEl = card; card.classList.add('dragging'); e.dataTransfer.effectAllowed = 'move'; try { e.dataTransfer.setData('text/plain', String(sc.id)); } catch (x) { /* */ } });
      card.addEventListener('dragend', () => { card.classList.remove('dragging'); grid.querySelectorAll('.drop-t').forEach((c) => c.classList.remove('drop-t')); dragEl = null; });
      card.addEventListener('dragover', (e) => { if (!dragEl || dragEl === card) return; e.preventDefault(); grid.querySelectorAll('.drop-t').forEach((c) => c.classList.remove('drop-t')); card.classList.add('drop-t'); });
      card.addEventListener('drop', (e) => {
        e.preventDefault(); if (!dragEl || dragEl === card) return;
        const kids = [...grid.children], from = kids.indexOf(dragEl), to = kids.indexOf(card);
        if (from < to) card.after(dragEl); else card.before(dragEl);
        card.classList.remove('drop-t');
        const ids = [...grid.children].map((c) => Number(c.dataset.id));
        lsSet('apm_dash_order', [...ids, ...savedOrder.filter((i) => !ids.includes(i))]);
      });
      const x = h('button', { type: 'button', class: 'thumb-x', title: 'Dashboard\'dan kaldır', 'aria-label': sc.name + ' kartını dashboard\'dan kaldır', onclick: (e) => { e.stopPropagation(); hideCard(sc.id); } }, '×');
      x.addEventListener('keydown', (e) => e.stopPropagation());
      card.append(x);
      return card;
    });
    grid.append(...cards);
    const restore = hiddenCount ? h('p', { class: 'muted small' }, `${hiddenCount} sayfa dashboard'dan kaldırıldı. `,
      h('button', { type: 'button', class: 'linklike', onclick: () => { lsSet('apm_dash_hidden', []); render(); } }, 'Tümünü geri getir')) : null;
    return h('section', null, secTitle('Kontrol edilen sayfalar', 'senaryolar'),
      h('div', { class: 'legend' }, h('span', { class: 'lg up' }, 'Sağlıklı'), h('span', { class: 'lg warn' }, 'Sayfa değişti (24 sa) / aralıklı hata'), h('span', { class: 'lg down' }, 'Son 3 kontrol başarısız / bağlı probe 2 kez erişemedi'), h('span', { class: 'lg unknown' }, 'Bekliyor')),
      grid, restore,
      h('p', { class: 'muted small' }, 'Kartları sürükleyerek sıralayın; sağ tık veya × ile dashboard\'dan kaldırın (senaryo silinmez). Görüntü, tarayıcı senaryolarının son kontrolündeki sayfadır (yalnızca son 10 çalıştırma saklanır). Büyütmek için tıklayın.'));
  }

  // İzlemeyi duraklat/başlat: yönetici parolası (API anahtarıyla girenlerde anahtar) yeniden istenir.
  function monitoringDialog(paused) {
    const useKey = !ME.account;
    const inp = h('input', { type: 'password', required: true, autocomplete: useKey ? 'off' : 'current-password', placeholder: useKey ? 'API anahtarınızı yeniden girin' : 'Yönetici parolanız' });
    const msg = h('p', { class: 'msg small' });
    let close;
    const f = h('form', { class: 'stack', onsubmit: async (e) => {
      e.preventDefault(); msg.textContent = '';
      try { await api('/api/monitoring', { method: 'PUT', body: { paused: !paused, password: useKey ? undefined : inp.value, api_key: useKey ? inp.value : undefined } }); close(); render(); }
      catch (err) { msg.className = 'msg error small'; msg.textContent = err.message; }
    } },
    h('p', { class: 'small' }, paused ? 'Tüm probe ve senaryolar, Keşfet ve alarm değerlendirmesi yeniden başlayacak.' : 'Tüm probe ve senaryolar durur, yeni Keşfet isteği alınmaz, alarm değerlendirmesi ve yeni bildirimler durur. Tek tek ayarlar korunur; yeniden başlatınca devam eder. Agent verisi alınmaya devam eder.'),
    h('p', { class: 'muted small' }, `Onaylamak için ${useKey ? 'API anahtarınızı' : 'parolanızı'} girin.`),
    inp, msg, h('div', { class: 'row' }, h('span'), h('button', { type: 'submit', class: paused ? 'primary' : 'danger' }, paused ? 'İzlemeyi başlat' : 'İzlemeyi duraklat')));
    close = openModal(paused ? 'İzleme başlatılsın mı?' : 'İzleme duraklatılsın mı?', f, true);
    inp.focus();
  }
  // ---- Genel bakış pano şablonları (v2.6.1) ----
  const DASH_WIDGETS = { kpis: 'Özet kutuları', pages: 'Kontrol edilen sayfalar', machines: 'Makineler · CPU/RAM/Disk', avail: 'Erişilebilirlik (24 sa)', status: 'Asset durumu', alerts24: 'Alarmlar (24 sa)', slowest: 'En yavaş hedefler', scenarios: 'Senaryo sağlığı', openalerts: 'Açık alarmlar',
    perf_apps: 'Uygulama performansı', perf_p95: 'En yavaş uygulamalar (p95)', perf_queries: 'Yavaş veritabanı sorguları', perf_timing: 'Gecikme kırılımı (HTTP)', surface: 'Saldırı yüzeyi sapmaları',
    probe_status: 'Probe durumu', probe_failing: 'Hatalı probe\'lar', probe_types: 'Probe türleri', unhealthy: 'Sağlıksız asset\'ler', groups: 'Gruplara göre asset durumu', topo_impact: 'Kök neden ve etkilenenler',
    net_ssl: 'SSL sertifikaları (tümü)', net_dns: 'DNS kontrolleri (tümü)', net_ntp: 'Saat sapması · NTP (tümü)', net_ports: 'Port erişimi · TCP/UDP (tümü)', net_ping: 'Ping · ICMP (tümü)', net_path: 'Yol izleme (tümü)', net_db: 'Veritabanı probe\'ları (tümü)' };
  const DASH_DEF = { kpis: 12, pages: 8, machines: 4, avail: 8, status: 4, alerts24: 6, slowest: 6, scenarios: 12, openalerts: 12, perf_apps: 8, perf_p95: 4, perf_queries: 6, perf_timing: 6, surface: 8, probe_status: 4, probe_failing: 8, probe_types: 4, unhealthy: 6, groups: 6, topo_impact: 6, net_ssl: 12, net_dns: 12, net_ntp: 12, net_ports: 12, net_ping: 12, net_path: 12, net_db: 12 };
  const DASH_SIZES = [4, 6, 8, 12];
  const DASH_TEMPLATES = [
    { id: 'operasyon', name: 'Operasyon (varsayılan)', w: [['kpis', 12], ['pages', 8], ['machines', 4], ['avail', 8], ['status', 4], ['alerts24', 6], ['slowest', 6], ['scenarios', 12], ['openalerts', 12]] },
    { id: 'performans', name: 'Performans', w: [['kpis', 12], ['perf_apps', 8], ['perf_p95', 4], ['perf_timing', 6], ['perf_queries', 6], ['slowest', 6], ['machines', 6], ['avail', 12]] },
    { id: 'guvenlik', name: 'Güvenlik', w: [['kpis', 12], ['surface', 8], ['net_ssl', 12], ['openalerts', 12], ['alerts24', 6], ['avail', 6]] },
    { id: 'noc', name: 'NOC (alarm odaklı)', w: [['kpis', 12], ['openalerts', 12], ['unhealthy', 6], ['topo_impact', 6], ['probe_status', 4], ['probe_failing', 8], ['machines', 12], ['pages', 12]] },
    { id: 'probe', name: 'Probe\'lar', w: [['kpis', 12], ['probe_status', 4], ['probe_types', 4], ['net_ssl', 12], ['probe_failing', 12], ['slowest', 6], ['avail', 6]] },
    { id: 'ag', name: 'Ağ ve altyapı servisleri', w: [['kpis', 12], ['net_ssl', 12], ['net_dns', 12], ['net_ports', 12], ['net_ping', 12], ['net_ntp', 12], ['net_path', 12], ['net_db', 12]] },
    { id: 'yonetici', name: 'Yönetici özeti', w: [['kpis', 12], ['avail', 8], ['status', 4], ['groups', 6], ['alerts24', 6], ['perf_p95', 6]] },
  ];
  const DASH = { edit: false, draft: null, base: null };
  function dashLoad() {
    const ok = (x) => x && typeof x.id === 'string' && typeof x.name === 'string' && Array.isArray(x.w);
    try {
      const v = JSON.parse(localStorage.getItem('apm_dash_layouts') || 'null');
      if (v && typeof v === 'object') {
        const custom = (Array.isArray(v.custom) ? v.custom : []).filter(ok).slice(0, 20)
          .map((x) => ({ id: x.id.slice(0, 20), name: x.name.slice(0, 40), w: x.w.map((y) => (Array.isArray(y) && y[0] === 'cert_expiry' ? ['net_ssl', 12] : y)).filter((y, i, arr) => Array.isArray(y) && DASH_WIDGETS[y[0]] && arr.findIndex((z) => Array.isArray(z) && z[0] === y[0]) === i).map((y) => [y[0], DASH_SIZES.includes(y[1]) ? y[1] : 12]) }));
        return { active: String(v.active || 'operasyon'), custom };
      }
    } catch (e) { /* depolama kapalı veya bozuk: varsayılan */ }
    return { active: 'operasyon', custom: [] };
  }
  function dashSave(s) { try { localStorage.setItem('apm_dash_layouts', JSON.stringify(s)); } catch (e) { /* yalnızca bu oturumda geçerli */ } }

  async function viewGenel() {
    const [o, d, alerts, allAssets] = await Promise.all([refreshHeader(), api('/api/dashboard'), api('/api/alerts?state=open'), api('/api/assets').catch(() => [])]);
    const st = o.by_status;
    const total = Object.values(st).reduce((a, b) => a + b, 0);
    const av = d.availability_24h;
    const crit = (d.alerts_by_severity || {}).critical || 0;
    const scOk = d.scenarios.filter((x) => x.runs.length && x.runs[x.runs.length - 1].ok).length;
    const seg = (k) => { const e = h('span', { class: k, title: `${STATUS[k]}: ${st[k] || 0} — listele` }); e.style.width = (total ? ((st[k] || 0) / total) * 100 : 0) + '%'; return clickable(e, () => goTo('assetler', { assetFilter: { status: k } }), `${STATUS[k]} assetleri listele`); };

    const kpis = h('div', { class: 'kpis' },
      kpi('Erişilebilirlik (24 saat)', av == null ? '—' : av.toFixed(av >= 99.95 ? 1 : 2) + '%', av == null ? 'veri yok' : 'probe ve senaryo ortalaması', miniLine(d.availability), av != null && av < 99 ? 'bad' : '', () => goTo('sla')),
      kpi('Açık alarm', String(o.open_alerts), crit ? `${crit} kritik` : 'kritik alarm yok', null, crit ? 'bad' : '', () => goTo('alarmlar')),
      kpi('Asset', `${st.up || 0} / ${total}`, `${st.down || 0} erişilemiyor, ${st.degraded || 0} kısmen erişilemiyor, ${st.offline || 0} çevrimdışı`, null, (st.down || 0) ? 'bad' : '', () => goTo('assetler', (st.down || 0) ? { assetFilter: { status: 'down' } } : undefined)),
      kpi('Ort. yanıt süresi (1 sa)', d.avg_latency_ms == null ? '—' : fmt(d.avg_latency_ms) + ' ms', 'probe gecikmesi', null, '', () => goTo('probelar')),
      kpi('Senaryolar', d.scenarios.length ? `${scOk} / ${d.scenarios.length}` : '—', d.scenarios.length ? 'son çalıştırması başarılı' : 'henüz senaryo yok', null, d.scenarios.length && scOk < d.scenarios.length ? 'bad' : '', () => goTo('senaryolar')));

    const availRows = d.availability.map((v, i) => h('tr', null, h('td', { class: 'mono' }, hourLabel(i)), h('td', { class: 'mono' }, v == null ? '—' : v.toFixed(2) + '%')));
    const alertRows = d.alerts_critical.map((c, i) => h('tr', null, h('td', { class: 'mono' }, hourLabel(i)), h('td', { class: 'mono' }, c), h('td', { class: 'mono' }, d.alerts_other[i])));

    const kindCounts = Object.entries(Object.entries(d.by_kind).reduce((m, [k, n]) => { const kk = k === 'url' ? 'website' : k; m[kk] = (m[kk] || 0) + n; return m; }, {})).sort((a, b) => b[1] - a[1]);
    const kindItems = kindCounts.slice(0, 7).map(([k, n], ix) => ({ label: KIND[k] || k, value: n, cls: 'c' + (ix + 1), icon: kindIcon(k, 18), go: () => goTo('assetler', { assetFilter: { kind: k } }) }));
    if (kindCounts.length > 7) kindItems.push({ label: 'Diğer', value: kindCounts.slice(7).reduce((a, x) => a + x[1], 0), cls: 'c8' });
    const statusPanel = h('div', { class: 'panel stack' },
      total ? donut(['up', 'degraded', 'down', 'offline', 'unknown'].map((k) => ({ label: STATUS[k], value: st[k] || 0, cls: k, go: () => goTo('assetler', { assetFilter: { status: k } }) })), { label: 'Asset durumu', sub: 'asset', showZero: false }) : h('p', { class: 'muted' }, 'Henüz asset yok.'),
      h('div', { class: 'sub-h' }, 'Türe göre'),
      kindItems.length ? donut(kindItems, { label: 'Asset türleri', sub: 'tür: ' + kindCounts.length }) : h('p', { class: 'muted' }, 'Henüz veri yok.'));

    const scenStat = { ok: 0, bad: 0, wait: 0 }; d.scenarios.forEach((sc) => { const l = sc.runs[sc.runs.length - 1]; if (!l) scenStat.wait++; else if (l.ok) scenStat.ok++; else scenStat.bad++; });
    const scen = d.scenarios.length ? h('div', { class: 'panel scn' }, d.scenarios.map((sc) => {
      const last = sc.runs[sc.runs.length - 1];
      const okMs = sc.runs.filter((r) => r.ok).map((r) => r.ms);
      const avg = okMs.length ? okMs.reduce((a, b) => a + b, 0) / okMs.length : null;
      return clickable(h('div', { class: 'scn-row' },
        h('div', { class: 'scn-name' }, h('b', null, sc.name), ' ', pill('info', sc.kind === 'browser' ? 'Tarayıcı' : 'HTTP'),
          sc.last_changed_at && Date.now() - new Date(sc.last_changed_at) < 864e5 ? [' ', h('a', { href: '#', class: 'pill-link', title: 'Değişiklik ayrıntıları', onclick: (e) => { e.preventDefault(); showChanges(sc); } }, pill('warning', 'sayfa değişti')), ' ', h('button', { type: 'button', class: 'small', onclick: async () => { await api(`/api/scenarios/${sc.id}/ack-changes`, { method: 'POST' }); render(); } }, 'Temizle')] : null,
          h('div', { class: 'muted small' }, sc.asset)),
        sc.runs.length ? strip(sc.runs) : h('span', { class: 'muted small' }, 'henüz çalışmadı'),
        h('div', { class: 'scn-stat' }, last ? (last.ok ? pill('up', 'Başarılı') : pill('down', 'Başarısız')) : pill('unknown', 'Bekliyor'),
          avg != null ? h('div', { class: 'mono small muted' }, `ort. ${fmt(avg)} ms`) : null)), () => goTo('senaryolar'), sc.name + ' — senaryolara git');
    }), h('p', { class: 'muted small' }, 'Her çubuk bir çalıştırma: yükseklik süreyi, kırmızı tam çubuk başarısızlığı gösterir (son 30).')) : null;
    const scenDonut = d.scenarios.length ? donutPanel('Senaryo durumu (son çalıştırma)', [{ label: 'Başarılı', value: scenStat.ok, cls: 'up', go: () => goTo('senaryolar') }, { label: 'Başarısız', value: scenStat.bad, cls: 'down', go: () => goTo('senaryolar') }, { label: 'Bekliyor', value: scenStat.wait, cls: 'wait' }], { label: 'Senaryo durumu', sub: 'senaryo', solo: true }) : null;

    // CPU / RAM / Disk kaynak widget'ı: ölçümü gelen makineler.
    const machines = (Array.isArray(allAssets) ? allAssets : []).filter((a) => a.overview !== false && a.latest && ['host.cpu_pct', 'host.mem_pct', 'host.disk_pct'].some((k) => a.latest[k] != null))
      .sort((a, b) => Math.max(b.latest['host.cpu_pct'] || 0, b.latest['host.mem_pct'] || 0, b.latest['host.disk_pct'] || 0) - Math.max(a.latest['host.cpu_pct'] || 0, a.latest['host.mem_pct'] || 0, a.latest['host.disk_pct'] || 0));
    const meter = (label, v, detail) => {
      const lvl = v == null ? '' : v >= 90 ? 'crit' : v >= 75 ? 'warn' : 'ok';
      const fill = h('span', { class: 'fill ' + lvl }); fill.style.width = (v == null ? 0 : Math.min(100, v)) + '%';
      const m = h('div', { class: 'mtr', role: 'img', 'aria-label': `${label} ${v == null ? 'veri yok' : fmt(v) + '%'}` }, h('span', { class: 'mtr-l' }, label), h('span', { class: 'mtr-t' }, fill), h('span', { class: 'mtr-v mono' }, v == null ? '—' : fmt(v) + '%'));
      if (detail && v != null) {
        m.classList.add('go'); m.setAttribute('role', 'button'); m.setAttribute('tabindex', '0'); m.title = 'Tüm bağlama noktalarını göster';
        const open = (e) => { e.preventDefault(); e.stopPropagation(); detail(); };
        m.addEventListener('click', open);
        m.addEventListener('keydown', (e) => { if (e.key === 'Enter' || e.key === ' ') open(e); });
      }
      return m;
    };
    const machineWidget = machines.length ? h('section', { class: 'ov-mach' }, secTitle('Makineler · CPU / RAM / Disk', 'assetler'),
      h('div', { class: 'mach-grid' }, machines.map((a) => h('a', { href: '#assetler', class: 'panel mach', title: 'Asset ayrıntısı', onclick: (e) => { e.preventDefault(); goTo('assetler', { asset: a.id }); } },
        h('div', { class: 'row' }, h('span', { class: 'kl' }, kindIcon(a.kind, 26), h('b', null, a.name)), pill(a.status, STATUS[a.status])),
        meter('CPU', a.latest['host.cpu_pct']), meter('RAM', a.latest['host.mem_pct']), meter('Disk', a.latest['host.disk_pct'], () => diskDialog(a.id, a.name)),
        a.last_seen ? h('span', { class: 'muted small mono' }, 'son veri ' + ago(a.last_seen)) : null)))) : null;
    if (tv169) {
      const open = (Array.isArray(alerts) ? alerts : []).slice(0, 6);
      const clock = h('span', { class: 'mono tv-clock' });
      const tick = () => { clock.textContent = new Date().toLocaleString((window.LOCALE || 'tr-TR')); };
      tick(); clearInterval(tvTimer); tvTimer = setInterval(() => { if (!tv169) clearInterval(tvTimer); else tick(); }, 1000);
      return h('div', { class: 'tv-stage' },
        h('div', { class: 'tv-top' }, h('b', { class: 'tv-title' }, 'APP_MON · Genel bakış'), clock,
          h('button', { type: 'button', class: 'small', onclick: () => setTv(false) }, '✕ Çık')),
        kpis,
        h('div', { class: 'tv-mid' }, pageFrames(d.scenarios) || h('section', null, h('p', { class: 'muted' }, 'Tarayıcı senaryosu yok.')), machineWidget || h('section', null, h('p', { class: 'muted' }, 'Makine ölçümü yok.'))),
        h('section', { class: 'tv-alerts' }, h('h3', null, `Açık alarmlar (${(alerts || []).length})`), alertTable(open, false, true)));
    }
    const mon = o.monitoring || {};
    const monBtn = h('button', { type: 'button', class: 'small ' + (mon.paused ? 'primary' : ''), title: mon.paused ? 'Tüm probe, senaryo ve alarm değerlendirmesini yeniden başlat' : 'Tüm probe, senaryo, Keşfet ve alarm değerlendirmesini duraklat (ayarlar korunur)',
      onclick: () => monitoringDialog(!!mon.paused) }, mon.paused ? '▶ İzlemeyi başlat' : '⏸ İzlemeyi duraklat');
    // ---- Pano widget'ları (v2.6.1) ----
    const lazyW = (title, view, loader) => {
      const body = h('div', { class: 'panel' }, h('p', { class: 'muted small' }, 'Yükleniyor…'));
      loader().then((el) => body.replaceChildren(el)).catch(() => body.replaceChildren(h('p', { class: 'muted' }, 'Veri alınamadı.')));
      return h('section', null, secTitle(title, view), body);
    };
    const perfHint = () => h('p', { class: 'muted' }, 'Henüz erişim günlüğü verisi yok. Performans sayfasındaki "Nasıl bağlanır?" bölümüne bakın.');
    const WG = {
      kpis: () => kpis,
      pages: () => pageFrames(d.scenarios),
      machines: () => machineWidget,
      avail: () => h('section', null, secTitle('Erişilebilirlik · son 24 saat', 'sla'), h('div', { class: 'panel' }, availChart(d.availability),
        h('details', { class: 'adv' }, h('summary', null, 'Tablo olarak göster'), table(['Saat', 'Erişilebilirlik'], availRows, '')))),
      status: () => h('section', null, secTitle('Asset durumu', 'assetler'), statusPanel),
      alerts24: () => h('section', null, secTitle('Alarmlar · son 24 saat', 'alarmlar'), h('div', { class: 'panel' },
        h('div', { class: 'legend' }, h('span', { class: 'lg crit' }, 'Kritik'), h('span', { class: 'lg warn' }, 'Uyarı / bilgi')),
        alertsChart(d.alerts_critical, d.alerts_other),
        h('details', { class: 'adv' }, h('summary', null, 'Tablo olarak göster'), table(['Saat', 'Kritik', 'Diğer'], alertRows, '')))),
      slowest: () => h('section', null, secTitle('En yavaş hedefler · son 1 saat', 'probelar'), h('div', { class: 'panel' },
        d.slowest.length ? rankBars(d.slowest.map((x) => ({ kind: x.kind, label: x.asset, value: x.avg_ms, text: `${fmt(x.avg_ms)} ms (en yüksek ${fmt(x.max_ms)})`, go: () => goTo('assetler', { asset: x.asset_id }) })), ' ms') : h('p', { class: 'muted' }, 'Henüz probe gecikmesi verisi yok.'))),
      scenarios: () => (scen ? h('section', null, secTitle('Senaryo sağlığı', 'senaryolar'), scenDonut ? h('div', { class: 'scen-row' }, scenDonut, scen) : scen) : null),
      openalerts: () => h('section', null, secTitle('Açık alarmlar', 'alarmlar'), alertTable(alerts, false, true)),
      perf_apps: () => lazyW('Uygulama performansı · son 1 saat', 'performans', async () => {
        const p = await api('/api/perf/apps?window=1h');
        if (!p.apps.length) return perfHint();
        const rows = p.apps.slice(0, 8).map((a) => h('tr', { class: 'clickrow', tabindex: '0', onclick: () => goTo('performans') },
          h('td', null, a.app), h('td', { class: 'mono' }, fmt(a.req_per_min)), h('td', { class: 'mono' }, ppct(a.err_pct)), h('td', { class: 'mono' }, pms(a.p50_ms)), h('td', { class: 'mono' }, pms(a.p95_ms)), h('td', { class: 'mono' }, pms(a.p99_ms))));
        return table(['Uygulama', 'İstek/dk', 'Hata (5xx)', 'p50', 'p95', 'p99'], rows, '');
      }),
      perf_p95: () => lazyW('En yavaş uygulamalar (p95)', 'performans', async () => {
        const p = await api('/api/perf/apps?window=1h');
        const list = p.apps.filter((a) => a.p95_ms != null).sort((a, b) => b.p95_ms - a.p95_ms).slice(0, 6);
        if (!list.length) return perfHint();
        return rankBars(list.map((a) => ({ kind: 'service', label: a.app, value: a.p95_ms, text: `p95 ${pms(a.p95_ms)} · ${ppct(a.err_pct)} hata`, go: () => goTo('performans') })), ' ms');
      }),
      perf_queries: () => lazyW('Yavaş veritabanı sorguları', 'performans', async () => {
        const q = await api('/api/perf/queries');
        const list = [...q.queries].sort((a, b) => (b.total_ms || 0) - (a.total_ms || 0)).slice(0, 5);
        if (!list.length) return h('p', { class: 'muted' }, 'Henüz sorgu verisi yok. "PostgreSQL sağlık (pgstats)" probe\'u ekleyin.');
        const rows = list.map((x) => h('tr', { class: 'clickrow', tabindex: '0', onclick: () => { PERF.tab = 'queries'; goTo('performans'); } },
          h('td', null, x.asset), h('td', null, h('code', { class: 'perf-sql', title: x.query }, x.query.length > 70 ? x.query.slice(0, 70) + '…' : x.query)),
          h('td', { class: 'mono' }, String(x.calls)), h('td', { class: 'mono' }, pms(x.mean_ms)), h('td', null, x.slow ? pill('down', 'Yavaş') : '')));
        return table(['Veritabanı', 'Sorgu', 'Çağrı', 'Ortalama', ''], rows, '');
      }),
      perf_timing: () => lazyW('Gecikme kırılımı (HTTP)', 'performans', async () => {
        const t = await api('/api/perf/timing');
        const list = [...t.probes].sort((a, b) => ((b.timing && b.timing.total) || 0) - ((a.timing && a.timing.total) || 0)).slice(0, 5);
        if (!list.length) return h('p', { class: 'muted' }, 'Henüz kırılım verisi yok (HTTP probe\'ları çalıştıkça dolar).');
        const legend = h('div', { class: 'perf-legend' }, TIMING_PARTS.map(([, n, c]) => h('span', null, h('i', { class: 'sw ' + c }), n)));
        const rows = list.map((p) => {
          const tm = p.timing, total = tm.total || TIMING_PARTS.reduce((a, [k]) => a + (tm[k] || 0), 0) || 1;
          const bar = h('div', { class: 'perf-stack', role: 'img', 'aria-label': TIMING_PARTS.map(([k, n]) => `${n} ${pms(tm[k])}`).join(', ') },
            TIMING_PARTS.map(([k, n, c]) => { const w = ((tm[k] || 0) / total) * 100; return w > 0 ? sized(h('span', { class: c, title: `${n}: ${pms(tm[k])}` }), w) : null; }));
          return h('tr', { class: 'clickrow', tabindex: '0', onclick: () => { PERF.tab = 'timing'; goTo('performans'); } }, h('td', null, p.asset), h('td', null, bar), h('td', { class: 'mono' }, pms(tm.total)));
        });
        return h('div', { class: 'stack' }, legend, table(['Hedef', 'Dağılım', 'Toplam'], rows, ''));
      }),
      probe_status: () => lazyW('Probe durumu', 'probelar', async () => {
        const ps = await api('/api/probes');
        if (!ps.length) return h('p', { class: 'muted' }, 'Henüz probe tanımlanmadı.');
        const off = ps.filter((x) => !x.enabled).length, bad = ps.filter((x) => x.enabled && x.last_error).length;
        const wait = ps.filter((x) => x.enabled && !x.last_error && !x.last_run).length, ok = ps.length - off - bad - wait;
        return donut([{ label: 'Çalışıyor', value: ok, cls: 'up', go: () => goTo('probelar') }, { label: 'Hatalı', value: bad, cls: 'down', go: () => goTo('probelar') },
          { label: 'Bekliyor', value: wait, cls: 'unknown' }, { label: 'Kapalı', value: off, cls: 'offline' }], { label: 'Probe durumu', sub: 'probe', showZero: false });
      }),
      probe_failing: () => lazyW('Hatalı probe\'lar', 'probelar', async () => {
        const ps = (await api('/api/probes')).filter((x) => x.enabled && x.last_error).sort((a, b) => String(b.last_run || '').localeCompare(String(a.last_run || '')));
        if (!ps.length) return h('p', { class: 'muted' }, 'Hatalı probe yok.');
        const rows = ps.slice(0, 20).map((x) => h('tr', { class: 'clickrow', tabindex: '0', onclick: () => goTo('probelar') },
          h('td', null, x.asset), h('td', { class: 'mono small' }, x.type), h('td', { class: 'mono small', title: x.target }, x.target.length > 40 ? x.target.slice(0, 40) + '…' : x.target), h('td', { class: 'error small' }, x.last_error), h('td', { class: 'mono small' }, ago(x.last_run))));
        return table(['Asset', 'Tür', 'Hedef', 'Hata', 'Son'], rows, '', 8);
      }),
      probe_types: () => lazyW('Probe türleri', 'probelar', async () => {
        const ps = await api('/api/probes');
        if (!ps.length) return h('p', { class: 'muted' }, 'Henüz probe tanımlanmadı.');
        const by = {}; ps.forEach((x) => { by[x.type] = (by[x.type] || 0) + 1; });
        const e = Object.entries(by).sort((a, b) => b[1] - a[1]);
        const items = e.slice(0, 7).map(([k, n], ix) => ({ label: (PROBE_TYPES.find((t) => t[0] === k) || [k, k])[1].split(' — ')[0], value: n, cls: 'c' + (ix + 1), go: () => goTo('probelar') }));
        if (e.length > 7) items.push({ label: 'Diğer', value: e.slice(7).reduce((a, x) => a + x[1], 0), cls: 'c8' });
        return donut(items, { label: 'Probe türleri', sub: 'probe' });
      }),
      net_ssl: () => lazyW('SSL sertifikaları · tümü', 'probelar', dwSsl),
      net_dns: () => lazyW('DNS kontrolleri · tümü', 'performans', dwDns),
      net_ntp: () => lazyW('Saat sapması (NTP) · tümü', 'performans', dwNtp),
      net_ports: () => lazyW('Port erişimi (TCP/UDP) · tümü', 'performans', dwPorts),
      net_ping: () => lazyW('Ping (ICMP) · tümü', 'performans', dwPing),
      net_path: () => lazyW('Yol izleme · tümü', 'performans', dwPath),
      net_db: () => lazyW('Veritabanı probe\'ları · tümü', 'probelar', dwDb),
      unhealthy: () => {
        const bad = (Array.isArray(allAssets) ? allAssets : []).filter((a) => ['down', 'offline', 'degraded'].includes(a.status));
        const rows = bad.map((a) => h('tr', { class: 'clickrow', tabindex: '0', onclick: () => goTo('assetler', { asset: a.id }) },
          h('td', null, h('span', { class: 'kl' }, kindIcon(a.kind, 24), h('b', null, a.name))), h('td', null, pill(a.status, STATUS[a.status])), h('td', { class: 'mono small' }, ago(a.last_seen))));
        return h('section', null, secTitle('Sağlıksız asset\'ler', 'assetler'), h('div', { class: 'panel' }, rows.length ? table(['Asset', 'Durum', 'Son veri'], rows, '', 8) : h('p', { class: 'muted' }, 'Tüm asset\'ler sağlıklı.')));
      },
      groups: () => {
        const g = {}; (Array.isArray(allAssets) ? allAssets : []).forEach((a) => { const k = a.group || '(grupsuz)'; (g[k] = g[k] || { up: 0, bad: 0, n: 0 }).n++; if (a.status === 'up') g[k].up++; else if (['down', 'offline', 'degraded'].includes(a.status)) g[k].bad++; });
        const e = Object.entries(g).sort((a, b) => b[1].bad - a[1].bad || b[1].n - a[1].n).slice(0, 10);
        return h('section', null, secTitle('Gruplara göre asset durumu', 'assetler'), h('div', { class: 'panel' }, e.length
          ? table(['Grup', 'Asset', 'Çalışan', 'Sorunlu'], e.map(([k, v]) => h('tr', { class: 'clickrow', tabindex: '0', onclick: () => goTo('assetler', k === '(grupsuz)' ? undefined : { assetFilter: { group: k } }) },
            h('td', null, k), h('td', { class: 'mono' }, String(v.n)), h('td', { class: 'mono' }, String(v.up)), h('td', { class: 'mono' }, v.bad ? pill('down', String(v.bad)) : '0'))), '')
          : h('p', { class: 'muted' }, 'Henüz asset yok.')));
      },
      topo_impact: () => lazyW('Kök neden ve etkilenenler', 'topoloji', async () => {
        const t = await api('/api/topology');
        const nm = new Map(t.nodes.map((n) => [n.id, n]));
        const imp = t.nodes.filter((n) => n.impacted);
        if (!t.root_causes.length && !imp.length) return h('p', { class: 'muted' }, 'Bağımlılık zincirinde sorun yok.');
        return h('div', { class: 'stack' },
          h('div', { class: 'kpis' }, kpi('Olası kök neden', String(t.root_causes.length), 'çalışmayan', null, t.root_causes.length ? 'bad' : ''), kpi('Etkilenen', String(imp.length), 'bağımlı asset', null, imp.length ? 'warn' : '')),
          h('div', { class: 'facts' }, t.root_causes.slice(0, 5).map((id) => h('div', { class: 'fact' }, h('span', null, pill('down', 'Kök neden'), ' ', h('b', null, (nm.get(id) || {}).name)))),
            imp.slice(0, 6).map((n) => h('div', { class: 'fact' }, h('span', null, pill('warning', 'Etkileniyor'), ' ', h('b', null, n.name), h('span', { class: 'muted small' }, '  ← ' + (n.impacted_by || []).join(', ')))))));
      }),
      surface: () => lazyW('Saldırı yüzeyi sapmaları', 'yuzey', async () => {
        const e = await api('/api/surface/events?state=open');
        const rows = e.events.slice(0, 5).map((x) => h('tr', { class: 'clickrow', tabindex: '0', onclick: () => goTo('yuzey') },
          h('td', null, pill(x.severity, SEV[x.severity])), h('td', null, x.asset), h('td', null, SURF_KIND[x.kind] || x.kind), h('td', { class: 'mono' }, SURF_HEADER[x.item] || x.item), h('td', { class: 'mono small' }, ago(x.at))));
        return h('div', { class: 'stack' },
          h('div', { class: 'kpis' }, kpi('Gözden geçirilmemiş', String(e.open), 'sapma', null, e.open ? 'warn' : ''), kpi('Kritik', String(e.critical), 'sapma', null, e.critical ? 'bad' : '')),
          table(['Önem', 'Asset', 'Tür', 'Öğe', 'Zaman'], rows, 'Referanstan sapma yok.'));
      }),
    };

    // ---- Pano şablonları: hazır şablon seç, düzenle, kendi panonu kaydet ----
    const st0 = dashLoad();
    const allTpl = [...DASH_TEMPLATES, ...st0.custom];
    const cur = allTpl.find((x) => x.id === st0.active) || allTpl[0];
    if (DASH.edit && DASH.base !== cur.id) { DASH.edit = false; DASH.draft = null; }
    const layout = DASH.edit && DASH.draft ? DASH.draft : cur.w.map((x) => [x[0], x[1]]);
    const isCustom = !DASH_TEMPLATES.some((x) => x.id === cur.id);
    const tplSel = h('select', { 'aria-label': 'Pano şablonu' },
      h('optgroup', { label: 'Hazır şablonlar' }, DASH_TEMPLATES.map((x) => h('option', { value: x.id }, x.name))),
      st0.custom.length ? h('optgroup', { label: 'Panolarım' }, st0.custom.map((x) => h('option', { value: x.id }, x.name))) : null);
    tplSel.value = cur.id;
    tplSel.addEventListener('change', () => { DASH.edit = false; DASH.draft = null; dashSave({ active: tplSel.value, custom: st0.custom }); render(); });
    const startEdit = () => { DASH.edit = true; DASH.base = cur.id; DASH.draft = cur.w.map((x) => [x[0], x[1]]); render(); };
    const stopEdit = () => { DASH.edit = false; DASH.draft = null; render(); };
    const saveAs = (overwrite) => {
      const clean = DASH.draft.filter((x) => DASH_WIDGETS[x[0]]);
      if (!clean.length) { window.alert('Panoda en az bir widget olmalı.'); return; }
      let state = dashLoad();
      if (overwrite && isCustom) {
        state.custom = state.custom.map((x) => (x.id === cur.id ? { ...x, w: clean } : x));
      } else {
        if (state.custom.length >= 20) { window.alert('En çok 20 pano kaydedilebilir.'); return; }
        const nm = window.prompt('Pano adı', isCustom ? cur.name + ' (kopya)' : cur.name + ' (özel)');
        if (nm === null) return;
        const name = nm.trim().slice(0, 40);
        if (!name) return;
        const id = 'c' + Date.now();
        state.custom = [...state.custom, { id, name, w: clean }];
        state.active = id;
      }
      dashSave(state); DASH.edit = false; DASH.draft = null; render();
    };
    const delBoard = () => {
      if (!window.confirm(`"${cur.name}" panosu silinsin mi?`)) return;
      const state = dashLoad();
      dashSave({ active: 'operasyon', custom: state.custom.filter((x) => x.id !== cur.id) }); DASH.edit = false; DASH.draft = null; render();
    };
    const move = (i, dlt) => { const j = i + dlt; if (j < 0 || j >= DASH.draft.length) return; const t = DASH.draft[i]; DASH.draft[i] = DASH.draft[j]; DASH.draft[j] = t; render(); };
    const resize = (i) => { const k = DASH_SIZES.indexOf(DASH.draft[i][1]); DASH.draft[i][1] = DASH_SIZES[(k + 1) % DASH_SIZES.length]; render(); };
    const drop = (i) => { DASH.draft.splice(i, 1); render(); };
    const cells = layout.map((x, i) => {
      const id = x[0], size = DASH_SIZES.includes(x[1]) ? x[1] : 12;
      if (!WG[id]) return null;
      const el = WG[id]();
      if (!el && !DASH.edit) return null;
      const box = h('div', { class: 'dw dw-' + size });
      if (DASH.edit) {
        box.classList.add('dw-ed');
        box.append(h('div', { class: 'dw-bar' }, h('b', { class: 'small' }, DASH_WIDGETS[id]),
          h('span', { class: 'row' },
            h('button', { type: 'button', class: 'small', title: 'Yukarı / öne al', 'aria-label': DASH_WIDGETS[id] + ' öne al', disabled: i === 0, onclick: () => move(i, -1) }, '▲'),
            h('button', { type: 'button', class: 'small', title: 'Aşağı / geriye al', 'aria-label': DASH_WIDGETS[id] + ' geriye al', disabled: i === layout.length - 1, onclick: () => move(i, 1) }, '▼'),
            h('button', { type: 'button', class: 'small', title: 'Genişliği değiştir', onclick: () => resize(i) }, 'Genişlik ' + size + '/12'),
            h('button', { type: 'button', class: 'small danger', title: 'Panodan kaldır', 'aria-label': DASH_WIDGETS[id] + ' panodan kaldır', onclick: () => drop(i) }, '×'))));
      }
      box.append(el || h('p', { class: 'muted small panel' }, DASH_WIDGETS[id] + ': şu an gösterilecek veri yok.'));
      return box;
    });
    const absent = Object.keys(DASH_WIDGETS).filter((k) => !layout.some((x) => x[0] === k));
    const tools = h('div', { class: 'dw-tools row' },
      h('label', { class: 'small' }, 'Pano ', tplSel),
      DASH.edit ? [
        h('span', { class: 'small muted' }, 'Düzenleniyor: ' + cur.name),
        isCustom ? h('button', { type: 'button', class: 'small primary', onclick: () => saveAs(true) }, 'Kaydet') : null,
        h('button', { type: 'button', class: 'small ' + (isCustom ? '' : 'primary'), onclick: () => saveAs(false) }, 'Yeni pano olarak kaydet…'),
        isCustom ? h('button', { type: 'button', class: 'small danger', onclick: delBoard }, 'Panoyu sil') : null,
        h('button', { type: 'button', class: 'small', onclick: stopEdit }, 'İptal')]
        : h('button', { type: 'button', class: 'small', title: 'Widget ekle/çıkar, sırala, boyutlandır', onclick: startEdit }, '✎ Panoyu düzenle'));
    const addRow = DASH.edit ? h('div', { class: 'panel dw-add' }, h('b', { class: 'small' }, 'Widget ekle: '),
      absent.length ? absent.map((k) => h('button', { type: 'button', class: 'small', onclick: () => { DASH.draft.push([k, DASH_DEF[k] || 6]); render(); } }, '+ ' + DASH_WIDGETS[k])) : h('span', { class: 'muted small' }, 'Tüm widget\'lar panoda.'),
      !isCustom ? h('p', { class: 'muted small' }, 'Hazır şablonlar değiştirilmez; değişikliğinizi "Yeni pano olarak kaydet" ile kendi panonuz yapın.') : null) : null;
    return h('div', { class: 'ov' + (DASH.edit ? ' dw-editing' : '') },
      h('section', null, h('div', { class: 'row head-row' }, h('h2', null, 'Genel bakış'),
        h('div', { class: 'row' }, h('span', { class: 'mon-state', title: mon.paused ? 'Probe, senaryo ve alarm değerlendirmesi duruyor' : 'Probe, senaryo ve alarm değerlendirmesi çalışıyor' }, pill(mon.paused ? 'degraded' : 'up', mon.paused ? 'İzleme duraklatıldı' : 'İzleme aktif')), ME.role === 'admin' ? monBtn : null,
          h('button', { type: 'button', class: 'small', title: 'Duvar ekranı / TV için 16:9 görünüm', onclick: () => setTv(true) }, '16:9 görünüm'))), tools),
      addRow,
      h('div', { class: 'dgrid' }, cells));
  }

  // ---- Ağ ve altyapı widget'ları (v3.17.0): ortak "özet şerit + kareler + detay paneli" ----
  // Her widget ilgili türdeki TÜM probe'ları kapsar. Pano üzerinde yalnızca özet şerit ve küçük kareler durur (50-500 probe sığar);
  // bir kareye ya da "Listeyi aç" düğmesine tıklayınca sağdan detay paneli açılır. SSL renkleri alarm kurallarına ve açık alarma bağlıdır.
  let netCache = { t: 0, p: null };
  async function netProbes() {
    if (!netCache.p || Date.now() - netCache.t > 20000) netCache = { t: Date.now(), p: api('/api/perf/network?window=1h') };
    try { return (await netCache.p).probes || []; } catch (e) { netCache = { t: 0, p: null }; throw e; }
  }
  const pdet = (p, k) => { const x = (p.detail || []).find((d) => d.k === k); return x ? x.v : ''; };
  const pst = (p) => (p.last_ok === true ? 'ok' : p.last_ok === false ? 'bad' : 'wait');
  const PST = { ok: ['up', 'Çalışıyor'], bad: ['down', 'Hata'], wait: ['unknown', 'Bekliyor'] };
  const PSTATES = [{ k: 'bad', cls: 'down', label: 'Hata' }, { k: 'wait', cls: 'unknown', label: 'Bekliyor' }, { k: 'ok', cls: 'up', label: 'Çalışıyor' }];
  const PORD = { bad: 0, wait: 1, ok: 2 };
  const shortT = (t, n) => (t.length > n ? t.slice(0, n) + '…' : t);
  const probeCell = (p) => h('td', null, cardLink(p.asset_id, p.asset), h('div', { class: 'mono small muted', title: p.target }, shortT(p.target, 46)));
  const gauge = (pct, lvl) => h('span', { class: 'mtr-t', role: 'img', 'aria-hidden': 'true' }, sized(h('span', { class: 'fill ' + lvl }), Math.max(2, Math.min(100, pct))));
  const goPerf = (tab) => () => { PERF.tab = tab; goTo('performans'); };
  const stCell = (p) => h('td', null, pill(PST[pst(p)][0], PST[pst(p)][1]), p.last_error ? h('div', { class: 'error small', title: p.last_error }, shortT(p.last_error, 60)) : null, h('div', { class: 'muted small' }, ago(p.last_run)));
  const netEmpty = (txt) => h('p', { class: 'muted' }, txt);
  const kpiRow = (kp) => h('div', { class: 'kpis cardk' }, kp);

  // Sağdan açılan panel: tek örnek; "← Liste" ile listeye dönülür.
  function sidePanel(title, wide) {
    document.querySelectorAll('.dr-ov').forEach((x) => x.remove());
    const ttl = h('h3', null, title);
    const back = h('button', { type: 'button', class: 'small', hidden: true }, '← Liste');
    const body = h('div', { class: 'dr-body' });
    const close = () => { ov.remove(); document.removeEventListener('keydown', onKey, true); window.removeEventListener('hashchange', close); };
    const onKey = (e) => { if (e.key === 'Escape') { e.stopPropagation(); close(); } };
    const box = h('aside', { class: 'dr' + (wide ? ' wide' : ''), role: 'dialog', 'aria-label': title, onclick: (e) => e.stopPropagation() },
      h('div', { class: 'dr-head' }, h('div', { class: 'row' }, back, ttl), h('button', { type: 'button', class: 'small', onclick: close, 'aria-label': 'Kapat' }, 'Kapat')), body);
    const ov = h('div', { class: 'dr-ov', onclick: close }, box);
    document.addEventListener('keydown', onKey, true);
    window.addEventListener('hashchange', close);
    document.body.append(ov);
    return {
      close,
      show(t, node, backFn) { ttl.textContent = t; box.setAttribute('aria-label', t); body.replaceChildren(node); back.hidden = !backFn; back.onclick = backFn || null; box.scrollTop = 0; },
    };
  }

  // Probe ayrıntısı: durum, temel alanlar, probe'un kendi ayrıntı satırları ve bağlantılar.
  function probeDetail(it) {
    const p = it.p;
    const pairs = [['Asset', p.asset], ['Hedef', p.target], ['Tür', p.type.toUpperCase()], ['Son çalıştırma', p.last_run ? ago(p.last_run) : '—']].concat(it.pairs || [], (p.detail || []).map((d) => [d.k, String(d.v)]));
    return h('div', { class: 'stack' },
      h('div', { class: 'row' }, pill(it.cls, it.label), it.badge || null),
      p.last_error ? h('p', { class: 'error small' }, p.last_error) : null,
      it.extra || null,
      h('table', { class: 'kv-table' }, h('tbody', null, pairs.map(([k, v]) => h('tr', null, h('th', { scope: 'row' }, k), h('td', null, v))))),
      h('div', { class: 'row' }, h('button', { type: 'button', class: 'small primary', onclick: () => { document.querySelectorAll('.dr-ov').forEach((x) => x.remove()); openCard(p.asset_id); } }, 'Asset kartı'),
        h('button', { type: 'button', class: 'small', onclick: () => { document.querySelectorAll('.dr-ov').forEach((x) => x.remove()); goTo('probelar'); } }, 'Probe\'lara git')));
  }

  // Ortak widget gövdesi. o: { title, states:[{k,cls,label}], items:[{p, st, cls, label, tip, flag:'al'|'ak'|'', pairs, extra, badge, cells}], headers, kpis, note, empty }
  // cls: durum rengi (cs-<cls>); st: durumun anahtarı (şerit/süzme için). Her öğe tek probe'dur; hiçbiri kırpılmaz.
  function fleet(o) {
    const items = o.items;
    let sel = null;
    const cnt = (k) => items.filter((i) => i.st === k).length;
    const vis = () => items.filter((i) => !sel || i.st === sel);
    const strip = h('div', { class: 'fstrip', role: 'group', 'aria-label': o.title });
    const legend = h('div', { class: 'flegend' });
    const grid = h('div', { class: 'heat', role: 'list' });
    const listBtn = h('button', { type: 'button', class: 'small' });
    const toggle = (k) => { sel = sel === k ? null : k; draw(); };
    const showItem = (pn, it, backFn) => pn.show(`${it.p.asset} · ${shortT(it.p.target, 40)}`, probeDetail(it), backFn);
    const openList = () => {
      const pn = sidePanel(o.title, true);
      const list = vis();
      const rows = list.map((it) => clickable(h('tr', null, ...it.cells()), () => showItem(pn, it, () => pn.show(o.title, tbl, null)), `${it.p.asset} — ayrıntı`));
      const tbl = table(o.headers, rows, '', PAGE);
      pn.show(o.title + (sel ? ' · ' + o.states.find((s) => s.k === sel).label : ''), tbl, null);
    };
    listBtn.addEventListener('click', openList);
    function draw() {
      const present = o.states.filter((s) => cnt(s.k));
      strip.classList.toggle('sel', !!sel);
      strip.replaceChildren(...present.map((s) => {
        const b = h('button', { type: 'button', class: 'fseg cs-' + s.cls, 'aria-pressed': String(sel === s.k), title: `${s.label}: ${cnt(s.k)}`, 'aria-label': `${s.label}: ${cnt(s.k)}` }, String(cnt(s.k)));
        b.style.flexGrow = String(cnt(s.k)); b.addEventListener('click', () => toggle(s.k)); return b;
      }));
      legend.replaceChildren(...o.states.map((s) => {
        const b = h('button', { type: 'button', class: 'lgchip', 'aria-pressed': String(sel === s.k), disabled: cnt(s.k) ? null : 'disabled' }, h('i', { class: 'lgsw cs-' + s.cls }), s.label, h('b', null, String(cnt(s.k))));
        b.addEventListener('click', () => toggle(s.k)); return b;
      }));
      grid.replaceChildren(...vis().map((it) => {
        const b = h('button', { type: 'button', class: 'cell cs-' + it.cls + (it.flag ? ' ' + it.flag : ''), title: it.tip, 'aria-label': it.tip, role: 'listitem' });
        b.addEventListener('click', () => { const pn = sidePanel(`${it.p.asset} · ${shortT(it.p.target, 40)}`); showItem(pn, it, null); });
        return b;
      }));
      listBtn.textContent = `Listeyi aç (${vis().length})`;
    }
    draw();
    return h('div', { class: 'stack' }, o.kpis ? kpiRow(o.kpis) : null, strip, h('div', { class: 'row flegend-row' }, legend, listBtn), grid, o.note ? h('p', { class: 'muted small' }, o.note) : null);
  }
  const probeItem = (p, extra) => { const s = pst(p); return Object.assign({ p, st: s, cls: PST[s][0], label: PST[s][1], tip: `${p.asset} · ${shortT(p.target, 60)} — ${PST[s][1]}`, cells: () => [probeCell(p), stCell(p)] }, extra || {}); };
  const avgOf = (p) => (!p.shared && p.stats && p.stats.AvgMS != null ? pms(p.stats.AvgMS) : '—');

  // SSL: renk, alarm kurallarına (probe.cert_days_left) ve açık alarmlara göre. Kural yoksa varsayılan eşikler: 14 gün uyarı, 3 gün kritik.
  const SEVRANK = { info: 1, warning: 2, critical: 3 };
  const SSL_ST = [{ k: 'exp', cls: 'down', label: 'Süresi doldu' }, { k: 'crit', cls: 'down', label: 'Kritik' }, { k: 'warn', cls: 'degraded', label: 'Uyarı' }, { k: 'info', cls: 'info', label: 'Bilgi' }, { k: 'ok', cls: 'up', label: 'Alarm yok' }, { k: 'unk', cls: 'unknown', label: 'Ölçülemedi' }];
  async function dwSsl() {
    const ps = (await netProbes()).filter((p) => p.type === 'tls');
    if (!ps.length) return netEmpty('Henüz TLS sertifika probe\'u yok. Probe\'lar sayfasından "TLS sertifikası" türünde probe ekleyin.');
    const [allRules, allAlerts, assets] = await Promise.all([api('/api/alert-rules').catch(() => []), api('/api/alerts?state=open').catch(() => []), api('/api/assets').catch(() => [])]);
    const rules = allRules.filter((r) => r.enabled && r.metric === 'probe.cert_days_left' && (r.comparator === '<' || r.comparator === '>'));
    const usingDefault = !rules.length;
    const eff = usingDefault ? [{ name: 'Varsayılan uyarı', comparator: '<', threshold: 14, severity: 'warning', asset_ids: [], group: '', tag: '' }, { name: 'Varsayılan kritik', comparator: '<', threshold: 3, severity: 'critical', asset_ids: [], group: '', tag: '' }] : rules;
    const alerts = allAlerts.filter((a) => a.metric === 'probe.cert_days_left');
    const byId = {}; (Array.isArray(assets) ? assets : []).forEach((a) => { byId[a.id] = a; });
    const applies = (r, p) => { const a = byId[p.asset_id] || {}; return (!(r.asset_ids || []).length || r.asset_ids.includes(p.asset_id)) && (!r.group || a.group === r.group) && (!r.tag || (a.tags || []).includes(r.tag)); };
    const now = Date.now();
    const rows = ps.map((p) => {
      const end = pdet(p, 'Geçerlilik bitişi');
      let days = end ? Math.floor((new Date(end + 'T23:59:59Z') - now) / 864e5) : null;
      if (days == null && !p.shared && p.last && p.last['probe.cert_days_left'] != null) days = Math.floor(p.last['probe.cert_days_left']);
      const hit = days == null ? [] : eff.filter((r) => applies(r, p) && (r.comparator === '<' ? days < r.threshold : days > r.threshold));
      const al = alerts.filter((a) => a.asset_id === p.asset_id);
      let sev = 0; hit.forEach((r) => { sev = Math.max(sev, SEVRANK[r.severity] || 0); }); al.forEach((a) => { sev = Math.max(sev, SEVRANK[a.severity] || 0); });
      const st = days == null ? 'unk' : days < 0 ? 'exp' : sev >= 3 ? 'crit' : sev === 2 ? 'warn' : sev === 1 ? 'info' : 'ok';
      const open = al.filter((a) => !a.silenced); const acked = al.length && al.every((a) => a.ack_by);
      const flag = al.length ? (acked ? 'ak' : 'al') : '';
      const S = SSL_ST.find((x) => x.k === st);
      const dtxt = days == null ? 'ölçülemedi' : days < 0 ? 'süresi doldu' : days + ' gün kaldı';
      const tip = `${p.asset} · ${shortT(p.target, 50)} — ${dtxt}${al.length ? (acked ? ' · alarm üstlenildi' : ' · alarm açık') : ''}`;
      return { p, days, end, st, cls: S.cls, label: S.label, flag, al, open, hit, tip, cn: pdet(p, 'Sertifika sahibi'), issuer: pdet(p, 'Veren'), ver: pdet(p, 'TLS sürümü') };
    }).sort((a, b) => (a.days == null) - (b.days == null) || a.days - b.days);
    const lvl = (st) => (st === 'exp' || st === 'crit' ? 'crit' : st === 'warn' ? 'warn' : 'ok');
    const items = rows.map((r) => Object.assign(r, {
      pairs: [['Sertifika sahibi', r.cn || '—'], ['Veren', r.issuer || '—'], ['Geçerlilik bitişi', r.end || '—'], ['Kalan süre', r.days == null ? '—' : r.days < 0 ? 'süresi doldu' : r.days + ' gün']].filter((x) => !(r.p.detail || []).some((d) => d.k === x[0])),
      badge: r.al.length ? pill(r.flag === 'ak' ? 'up' : 'down', r.flag === 'ak' ? 'Alarm üstlenildi' : 'Alarm açık') : null,
      extra: h('div', { class: 'stack' },
        r.al.length ? h('div', { class: 'panel stack' }, h('b', null, 'Açık alarm'), r.al.map((a) => h('div', { class: 'small' }, pill(a.severity, SEV[a.severity]), ' ', a.rule, ' · ', ago(a.opened_at), a.ack_by ? h('span', null, ' · ', h('b', null, a.ack_by), a.ack_note ? ': ' + a.ack_note : '') : ' · üstlenen yok', a.silenced ? ' · bakımda' : ''))) : null,
        h('p', { class: 'muted small' }, 'Renk kuralları: ', (usingDefault ? eff : eff.filter((x) => applies(x, r.p))).map((x) => `${SEV[x.severity]} ${x.comparator} ${x.threshold} gün`).join(' · ') || 'bu sertifikaya uygulanan kural yok', usingDefault ? ' (varsayılan; alarm kuralı tanımlı değil)' : '')),
      cells: () => [probeCell(r.p), h('td', { class: 'small' }, r.cn || '—', r.issuer ? h('div', { class: 'muted' }, r.issuer) : null), h('td', { class: 'mono small' }, r.end || '—', r.ver ? h('div', { class: 'muted' }, r.ver) : null),
        h('td', { class: 'certcell' }, r.days == null ? h('span', { class: 'muted small' }, pst(r.p) === 'bad' ? 'bağlanılamadı' : 'bekliyor') : [h('b', { class: 'mono' }, r.days < 0 ? 'doldu' : r.days + ' gün'), gauge(r.days < 0 ? 0 : (r.days / 90) * 100, lvl(r.st))]),
        h('td', null, pill(r.cls, r.label), r.al.length ? h('div', { class: 'small' }, r.flag === 'ak' ? 'alarm üstlenildi' : 'alarm açık') : null)],
    }));
    const c = (k) => rows.filter((r) => r.st === k).length;
    const openAl = rows.filter((r) => r.flag === 'al').length;
    const soon = rows.filter((r) => r.days != null && r.days >= 0 && r.days <= 30).length;
    const nearest = rows.find((r) => r.days != null);
    const kp = [kpi('Toplam sertifika', String(rows.length), 'izlenen TLS probe', null, ''),
      kpi('En yakın bitiş', nearest ? (nearest.days < 0 ? 'doldu' : nearest.days + ' gün') : '—', nearest ? nearest.p.asset : 'ölçüm yok', null, nearest && nearest.st !== 'ok' ? (lvl(nearest.st) === 'crit' ? 'bad' : 'warn') : ''),
      kpi('30 gün içinde dolacak', String(soon), soon ? 'yenileme planlayın' : 'yok', null, soon ? 'warn' : ''),
      kpi('Açık alarm', String(openAl), openAl ? 'üstlenilmemiş' : 'yok', null, openAl ? 'bad' : ''), kpi('Süresi dolmuş', String(c('exp')), c('exp') ? 'acil' : 'yok', null, c('exp') ? 'bad' : '')];
    return fleet({ title: 'SSL sertifikaları', states: SSL_ST, items, kpis: kp, headers: ['Sertifika', 'Sahibi / veren', 'Bitiş', 'Kalan süre (0–90 gün)', 'Durum / alarm'],
      note: usingDefault ? 'Alarm kuralı tanımlı değil; renkler varsayılan eşiklere göredir (14 gün uyarı, 3 gün kritik). Alarmlar › Kurallar bölümünden "Sertifika süresi doluyor" kuralını ekleyebilirsiniz.' : 'Renkler "probe.cert_days_left" alarm kurallarının eşiklerine ve açık alarmlara göredir. Karedeki dolu nokta: alarm açık, boş halka: alarm üstlenildi.' });
  }

  // DNS: her kayıt türü ve çözümleyici için sonuç; başarısızlar önce gelir.
  async function dwDns() {
    const ps = (await netProbes()).filter((p) => p.type === 'dns').sort((a, b) => PORD[pst(a)] - PORD[pst(b)]);
    if (!ps.length) return netEmpty('Henüz DNS probe\'u yok. Probe\'lar sayfasından "DNS" türünde probe ekleyin.');
    const bad = ps.filter((p) => pst(p) === 'bad').length;
    const servers = new Set(ps.map((p) => p.server || 'sistem çözümleyicisi'));
    const items = ps.map((p) => probeItem(p, {
      tip: `${p.asset} · ${shortT(p.target, 60)} — ${PST[pst(p)][1]} · ${p.record || 'A'}`,
      pairs: [['Kayıt türü', p.record || 'A'], ['DNS sunucusu', p.server || 'sistem']],
      cells: () => [probeCell(p), h('td', { class: 'mono small' }, p.record || 'A'), h('td', { class: 'mono small' }, p.server || 'sistem'),
        h('td', { class: 'mono small' }, shortT(pdet(p, 'Cevaplar') || '—', 70), pdet(p, 'Dönen kayıt sayısı') ? h('div', { class: 'muted' }, pdet(p, 'Dönen kayıt sayısı') + ' kayıt') : null), h('td', { class: 'mono' }, avgOf(p)), stCell(p)],
    }));
    return fleet({ title: 'DNS kontrolleri', states: PSTATES, items, headers: ['Sorgu', 'Tür', 'Sunucu', 'Cevaplar', 'Süre (1 sa)', 'Durum'],
      kpis: [kpi('DNS probe', String(ps.length), 'izlenen sorgu', null, ''), kpi('Hatalı', String(bad), bad ? 'çözümlenemiyor' : 'hepsi çözümleniyor', null, bad ? 'bad' : ''), kpi('Çözümleyici', String(servers.size), 'farklı DNS sunucusu', null, '')] });
  }

  // NTP: işaretli saat sapması; ≥100 ms uyarı, ≥500 ms kritik.
  async function dwNtp() {
    const ps = (await netProbes()).filter((p) => p.type === 'ntp');
    if (!ps.length) return netEmpty('Henüz NTP probe\'u yok. Probe\'lar sayfasından "NTP (saat sapması)" türünde probe ekleyin.');
    const rows = ps.map((p) => ({ p, off: parseFloat(pdet(p, 'Saat sapması (işaretli)')), st: pdet(p, 'Stratum') })).sort((a, b) => (Number.isNaN(a.off) - Number.isNaN(b.off)) || Math.abs(b.off) - Math.abs(a.off));
    const lvl = (v) => (Number.isNaN(v) ? 'unk' : Math.abs(v) < 100 ? 'ok' : Math.abs(v) < 500 ? 'warn' : 'crit');
    const ST = [{ k: 'crit', cls: 'down', label: 'Yüksek sapma (≥500 ms)' }, { k: 'warn', cls: 'degraded', label: 'Sapıyor (100–500 ms)' }, { k: 'ok', cls: 'up', label: 'Normal (<100 ms)' }, { k: 'unk', cls: 'unknown', label: 'Ölçülemedi' }];
    const items = rows.map((r) => { const k = lvl(r.off); const S = ST.find((x) => x.k === k);
      const pct = Number.isNaN(r.off) ? 0 : Math.min(50, (Math.abs(r.off) / 500) * 50);
      const bar = h('span', { class: 'dvg', role: 'img', 'aria-label': Number.isNaN(r.off) ? 'ölçülemedi' : fmt(r.off) + ' ms' }, h('i', { class: 'dvg-mid' }),
        Number.isNaN(r.off) ? null : (() => { const f = h('i', { class: 'dvg-f ' + k }); f.style.width = pct + '%'; if (r.off < 0) f.style.right = '50%'; else f.style.left = '50%'; return f; })());
      return { p: r.p, st: k, cls: S.cls, label: S.label, tip: `${r.p.asset} — ${Number.isNaN(r.off) ? 'ölçülemedi' : fmt(r.off) + ' ms'}`,
        pairs: [['Saat sapması', Number.isNaN(r.off) ? '—' : (r.off > 0 ? '+' : '') + fmt(r.off) + ' ms'], ['Stratum', r.st || '—']].filter((x) => !(r.p.detail || []).some((d) => d.k === x[0])),
        extra: h('div', { class: 'stack' }, bar, h('p', { class: 'muted small' }, 'Çubuk orta çizgiden sağa (sunucu saati ileride) veya sola (geride) uzar; ±500 ms ölçeklidir.')),
        cells: () => [probeCell(r.p), h('td', { class: 'mono' }, Number.isNaN(r.off) ? '—' : (r.off > 0 ? '+' : '') + fmt(r.off) + ' ms'), h('td', null, bar.cloneNode(true)), h('td', { class: 'mono' }, r.st || '—'), stCell(r.p)] }; });
    const worst = rows.find((r) => !Number.isNaN(r.off));
    return fleet({ title: 'Saat sapması (NTP)', states: ST, items, headers: ['NTP kaynağı', 'Sapma', 'Sapma göstergesi (±500 ms)', 'Stratum', 'Durum'],
      kpis: [kpi('NTP probe', String(rows.length), 'izlenen saat kaynağı', null, ''), kpi('En büyük sapma', worst ? fmt(Math.abs(worst.off)) + ' ms' : '—', worst ? worst.p.asset : 'ölçüm yok', null, worst && Math.abs(worst.off) >= 500 ? 'bad' : worst && Math.abs(worst.off) >= 100 ? 'warn' : '')] });
  }

  // TCP/UDP port erişimi.
  async function dwPorts() {
    const ps = (await netProbes()).filter((p) => p.type === 'tcp' || p.type === 'udp').sort((a, b) => PORD[pst(a)] - PORD[pst(b)]);
    if (!ps.length) return netEmpty('Henüz TCP/UDP probe\'u yok. Probe\'lar sayfasından "TCP bağlantı" veya "UDP" türünde probe ekleyin.');
    const bad = ps.filter((p) => pst(p) === 'bad').length;
    const items = ps.map((p) => probeItem(p, {
      tip: `${p.asset} · ${shortT(p.target, 60)} — ${PST[pst(p)][1]} · ${p.type.toUpperCase()}`,
      pairs: [['Süre (1 sa ort.)', avgOf(p)], ['Erişilebilirlik', !p.shared && p.stats && p.stats.UpPct != null ? ppct(p.stats.UpPct) : '—']],
      cells: () => [probeCell(p), h('td', null, pill('info', p.type.toUpperCase())), h('td', { class: 'mono' }, avgOf(p)), h('td', { class: 'mono' }, !p.shared && p.stats && p.stats.UpPct != null ? ppct(p.stats.UpPct) : '—'), stCell(p)],
    }));
    return fleet({ title: 'Port erişimi', states: PSTATES, items, headers: ['Hedef', 'Tür', 'Süre (1 sa)', 'Erişilebilirlik', 'Durum'],
      kpis: [kpi('Port probe', String(ps.length), `${ps.filter((p) => p.type === 'tcp').length} TCP · ${ps.filter((p) => p.type === 'udp').length} UDP`, null, ''), kpi('Erişilemeyen', String(bad), bad ? 'kapalı ya da filtreli' : 'hepsi erişilebilir', null, bad ? 'bad' : '')] });
  }

  // ICMP ping: paket kaybı ve gecikme.
  async function dwPing() {
    const ps = (await netProbes()).filter((p) => p.type === 'icmp');
    if (!ps.length) return netEmpty('Henüz ICMP (ping) probe\'u yok. Probe\'lar sayfasından "ICMP (ping)" türünde probe ekleyin.');
    const rows = ps.map((p) => ({ p, loss: parseFloat(pdet(p, 'Paket kaybı')), rtt: !p.shared && p.stats ? p.stats.AvgMS : null })).sort((a, b) => (PORD[pst(a.p)] - PORD[pst(b.p)]) || (b.loss || 0) - (a.loss || 0));
    const maxRtt = Math.max(1, ...rows.map((r) => r.rtt || 0));
    const lossy = rows.filter((r) => r.loss > 0).length;
    const items = rows.map((r) => probeItem(r.p, {
      tip: `${r.p.asset} · ${shortT(r.p.target, 60)} — ${PST[pst(r.p)][1]}${Number.isNaN(r.loss) ? '' : ' · kayıp ' + r.loss + '%'}`,
      pairs: [['Paket kaybı', Number.isNaN(r.loss) ? '—' : r.loss + '%'], ['Gecikme (1 sa ort.)', r.rtt == null ? '—' : pms(r.rtt)]].filter((x) => !(r.p.detail || []).some((d) => d.k === x[0])),
      cells: () => [probeCell(r.p), h('td', { class: 'certcell' }, Number.isNaN(r.loss) ? '—' : [h('b', { class: 'mono' }, r.loss + '%'), gauge(r.loss, r.loss >= 20 ? 'crit' : r.loss > 0 ? 'warn' : 'ok')]),
        h('td', { class: 'certcell' }, r.rtt == null ? '—' : [h('b', { class: 'mono' }, pms(r.rtt)), gauge((r.rtt / maxRtt) * 100, r.rtt >= 200 ? 'warn' : 'ok')]), stCell(r.p)],
    }));
    return fleet({ title: 'Ping (ICMP)', states: PSTATES, items, headers: ['Hedef', 'Paket kaybı', 'Gecikme (1 sa ort.)', 'Durum'],
      kpis: [kpi('Ping probe', String(ps.length), 'izlenen hedef', null, ''), kpi('Paket kaybı olan', String(lossy), lossy ? 'ağ kalitesi düşük' : 'kayıp yok', null, lossy ? 'warn' : '')] });
  }

  // Yol izleme: sıçrama sayısı, rota değişimi.
  async function dwPath() {
    const ps = (await netProbes()).filter((p) => p.type === 'trace');
    if (!ps.length) return netEmpty('Henüz yol izleme probe\'u yok. Probe\'lar sayfasından "Yol izleme" türünde probe ekleyin.');
    const lv = (p) => (!p.shared && p.last ? p.last : {});
    const chg = (p) => lv(p)['net.path.changed'] === 1;
    const ST = [{ k: 'bad', cls: 'down', label: 'Hata' }, { k: 'chg', cls: 'degraded', label: 'Rota değişti' }, { k: 'wait', cls: 'unknown', label: 'Bekliyor' }, { k: 'ok', cls: 'up', label: 'Çalışıyor' }];
    const key = (p) => (pst(p) === 'bad' ? 'bad' : chg(p) ? 'chg' : pst(p));
    const sorted = ps.slice().sort((a, b) => ST.findIndex((x) => x.k === key(a)) - ST.findIndex((x) => x.k === key(b)));
    const maxH = Math.max(1, ...ps.map((p) => lv(p)['net.path.hops'] || 0));
    const items = sorted.map((p) => { const l = lv(p); const k = key(p); const S = ST.find((x) => x.k === k);
      return { p, st: k, cls: S.cls, label: S.label, tip: `${p.asset} · ${shortT(p.target, 60)} — ${S.label}${l['net.path.hops'] != null ? ' · ' + l['net.path.hops'] + ' sıçrama' : ''}`,
        pairs: [['Sıçrama', l['net.path.hops'] != null ? String(l['net.path.hops']) : '—'], ['RTT', l['net.path.rtt_ms'] != null ? pms(l['net.path.rtt_ms']) : '—'], ['Kayıp', l['net.path.loss_pct'] != null ? l['net.path.loss_pct'] + '%' : '—']],
        cells: () => [probeCell(p), h('td', { class: 'certcell' }, l['net.path.hops'] == null ? '—' : [h('b', { class: 'mono' }, String(l['net.path.hops'])), gauge((l['net.path.hops'] / maxH) * 100, 'ok')]),
          h('td', { class: 'mono' }, l['net.path.rtt_ms'] != null ? pms(l['net.path.rtt_ms']) : '—'), h('td', { class: 'mono' }, l['net.path.loss_pct'] != null ? l['net.path.loss_pct'] + '%' : '—'),
          h('td', null, chg(p) ? pill('warning', 'Rota değişti') : l['net.path.changed'] === 0 ? pill('up', 'Sabit') : '—'), stCell(p)] }; });
    const changed = ps.filter(chg).length;
    return fleet({ title: 'Yol izleme', states: ST, items, headers: ['Hedef', 'Sıçrama', 'RTT', 'Kayıp', 'Rota', 'Durum'],
      kpis: [kpi('Yol probe', String(ps.length), 'izlenen rota', null, ''), kpi('Rotası değişen', String(changed), changed ? 'yol değişti' : 'rotalar sabit', null, changed ? 'warn' : '')],
      note: 'Aynı asset üzerinde birden çok yol probe\'u varsa sıçrama/RTT değerleri ayırt edilemediği için "—" görünür.' });
  }

  // Veritabanı: PostgreSQL erişim ve sağlık probe'ları.
  async function dwDb() {
    const ps = (await api('/api/probes')).filter((p) => p.type === 'postgres' || p.type === 'pgstats');
    if (!ps.length) return netEmpty('Henüz veritabanı probe\'u yok. Probe\'lar sayfasından "PostgreSQL" türünde probe ekleyin.');
    const st = (p) => (!p.enabled ? 'off' : p.last_error ? 'bad' : p.last_run ? 'ok' : 'wait');
    const ST = [{ k: 'bad', cls: 'down', label: 'Hata' }, { k: 'wait', cls: 'unknown', label: 'Bekliyor' }, { k: 'ok', cls: 'up', label: 'Çalışıyor' }, { k: 'off', cls: 'offline', label: 'Kapalı' }];
    const sorted = ps.slice().sort((a, b) => ST.findIndex((x) => x.k === st(a)) - ST.findIndex((x) => x.k === st(b)));
    const bad = ps.filter((p) => st(p) === 'bad').length;
    const items = sorted.map((p) => { const k = st(p); const S = ST.find((x) => x.k === k);
      return { p: Object.assign({ detail: [] }, p), st: k, cls: S.cls, label: S.label, tip: `${p.asset} · ${p.type} — ${S.label}`, pairs: [['Probe türü', p.type === 'pgstats' ? 'Sağlık (pgstats)' : 'Erişim']],
        cells: () => [h('td', null, cardLink(p.asset_id, p.asset), h('div', { class: 'mono small muted', title: p.target }, shortT(p.target, 46))), h('td', null, pill('info', p.type === 'pgstats' ? 'Sağlık' : 'Erişim')),
          h('td', null, pill(S.cls, S.label), p.last_error ? h('div', { class: 'error small', title: p.last_error }, shortT(p.last_error, 70)) : null), h('td', { class: 'small' }, ago(p.last_run))] }; });
    return fleet({ title: 'Veritabanları', states: ST, items, headers: ['Veritabanı', 'Tür', 'Durum', 'Son kontrol'],
      kpis: [kpi('Veritabanı probe', String(ps.length), `${ps.filter((p) => p.type === 'pgstats').length} sağlık (pgstats)`, null, ''), kpi('Hatalı', String(bad), bad ? 'erişilemiyor' : 'hepsi erişilebilir', null, bad ? 'bad' : '')],
      note: 'Sorgu performansı için Performans › Veritabanı sorguları sekmesine bakın.' });
  }

  // Bölüm başlığı: ilgili sayfaya giden bağlantı ("→" ile).
  function secTitle(text, view, opts) {
    return h('h3', null, h('a', { href: '#' + view, class: 'sec-link', title: 'Ayrıntıya git', onclick: (e) => { e.preventDefault(); goTo(view, opts); } }, text, ' →'));
  }

  // Alarm için "Neden?" paneli: değişiklik-nedensellik motorunun (GET /api/alerts/{id}/rca) çıktısını gösterir.
  const RCA_CONF = { high: ['down', 'Yüksek'], medium: ['warning', 'Orta'], low: ['unknown', 'Düşük'] };
  function rcaLead(sec) {
    if (sec <= -30) return `alarmdan ${Math.max(1, Math.round(-sec / 60))} dk sonra başladı`;
    return sec < 90 ? 'alarmla aynı anda başladı' : `alarmdan ${Math.round(sec / 60)} dk önce başladı`;
  }
  function rcaPanel(d, cols) {
    const box = h('div', { class: 'panel stack rca' });
    if (!d.causes.length) box.append(h('p', { class: 'muted' }, 'Alarm öncesinde belirgin bir değişim bulunamadı. Ölçüm geçmişi yetersiz olabilir veya neden izlenen ölçümlerin dışındadır.'));
    else {
      const t = d.causes[0];
      const [cls, cl] = RCA_CONF[t.confidence] || RCA_CONF.low;
      box.append(h('p', null, h('b', null, 'En olası neden: '), `${t.asset} › ${t.metric}: ${fmt(t.before)} → ${fmt(t.after)}, ${rcaLead(t.lead_sec)}. `, pill(cls, cl + ' güven')));
      box.append(table(['Asset', 'Ölçüm', 'Değişim', 'Başlangıç', 'İlişki', 'Güven'], d.causes.map((c) => {
        const [k2, l2] = RCA_CONF[c.confidence] || RCA_CONF.low;
        return h('tr', null, h('td', null, c.asset), h('td', { class: 'mono small' }, c.metric), h('td', { class: 'mono' }, `${fmt(c.before)} → ${fmt(c.after)}`),
          h('td', { class: 'small' }, rcaLead(c.lead_sec)), h('td', { class: 'small' }, c.depth === 0 ? 'Alarmlı asset' : `Bağımlılık (seviye ${c.depth})`), h('td', null, pill(k2, l2)));
      }), '', 0));
    }
    if (d.changes.length) {
      box.append(h('h4', null, 'Bu sürede yapılan yönetim değişiklikleri'),
        h('ul', { class: 'small' }, d.changes.map((c) => h('li', null, h('span', { class: 'mono' }, ago(c.at)), ' · ', c.actor, ' · ', c.action + (c.detail ? ' ' + c.detail : '')))));
    }
    box.append(h('p', { class: 'muted small' }, `${d.checked_assets} asset incelendi (alarmlı asset ve bağımlılıkları). Bu bir tahmindir: ölçümlerdeki ani değişimler zaman sırası ve topolojiyle ilişkilendirilir, kesin kanıt değildir.`));
    return h('tr', { class: 'rca-row' }, h('td', { colspan: String(cols) }, box));
  }

  // Kural bazlı özet (D1): aynı kuraldan çok alarm varsa düğmeler listeyi o kurala göre süzer.
  function ruleChips(list, tbl) {
    const m = {};
    list.forEach((a) => { m[a.rule] = (m[a.rule] || 0) + 1; });
    const ents = Object.entries(m).sort((a, b) => b[1] - a[1]);
    if (list.length < 12 || ents.length < 2 && list.length < 12) return null;
    const pick = (r) => { const q = tbl.querySelector('.tblq'); if (q) { q.value = r; q.dispatchEvent(new Event('input')); } };
    return h('div', { class: 'row' }, h('span', { class: 'muted small' }, 'Kurala göre süz:'),
      ents.slice(0, 10).map(([r, n]) => h('button', { type: 'button', class: 'small', onclick: () => pick(r) }, `${r} ×${n}`)),
      h('button', { type: 'button', class: 'small', onclick: () => pick('') }, 'Tümü'));
  }
  function alertTable(list, withResolved, link) {
    const head = ['Önem', 'Kural', 'Asset', 'Değer', 'Açılış'].concat(withResolved ? ['Kapanış'] : ['Durum']);
    const ackBtn = (a) => h('button', { type: 'button', class: 'small', onclick: async () => {
      const note = window.prompt('Not (isteğe bağlı) — örn. "VPN hattını kontrol ediyorum"', '');
      if (note === null) return;
      try { await api(`/api/alerts/${a.id}/ack`, { method: 'POST', body: { note } }); render(); } catch (e) { alert(e.message); }
    } }, 'Üstleniyorum');
    const hl = focusAlertId;
    const hlIdx = hl == null ? -1 : list.findIndex((a) => a.id === hl);
    const t = table(head, list.map((a) => { const tr = h('tr', { class: a.id === hl ? 'hl' : '', 'data-aid': String(a.id) },
      h('td', null, pill(a.severity, SEV[a.severity])),
      h('td', null, a.rule),
      h('td', null, a.asset_id ? cardLink(a.asset_id, a.asset) : a.asset),
      h('td', { class: 'mono' }, `${fmt(a.value)} (${a.comparator} ${a.threshold})`),
      h('td', { class: 'mono small' }, ago(a.opened_at)),
      withResolved ? h('td', { class: 'mono small' }, a.resolved_at ? ago(a.resolved_at) : '—')
        : h('td', null, a.silenced ? [pill('info', 'bakımda'), ' '] : null,
            a.ack_by ? h('span', null, pill('up', 'üstlenildi'), ' ', h('span', { class: 'small' }, a.ack_by + (a.ack_note ? ': ' + a.ack_note : '')), ' ',
              h('button', { type: 'button', class: 'small linklike', onclick: async () => { try { await api(`/api/alerts/${a.id}/ack`, { method: 'DELETE' }); render(); } catch (e) { alert(e.message); } } }, 'geri al')) : ackBtn(a)));
      const why = h('button', { type: 'button', class: 'small', onclick: async (e) => {
        e.stopPropagation();
        const nx = tr.nextElementSibling;
        if (nx && nx.classList.contains('rca-row')) { nx.remove(); return; }
        try { tr.after(rcaPanel(await api(`/api/alerts/${a.id}/rca`), head.length)); } catch (er) { alert(er.message); }
      } }, 'Neden?');
      tr.lastElementChild.append(' ', why);
      return link ? clickable(tr, () => goTo('alarmlar', { alert: a.id }), `${a.rule} · ${a.asset} — alarmlara git`) : tr; }),
      'Açık alarm yok.', PAGE, hlIdx);
    if (hl != null) setTimeout(() => { const r = t.querySelector('tr.hl'); if (r) { r.scrollIntoView({ block: 'center', behavior: 'smooth' }); focusAlertId = null; } }, 60);
    return t;
  }

  async function viewAssetler() {
    await refreshHeader();
    const assets = await api('/api/assets');
    const grpList = h('datalist', { id: 'grp-list' }, groupsOf(assets).map((g) => h('option', { value: g })));
    const detail = h('section', { id: 'detail' });
    const open = (id) => { const a = assets.find((x) => x.id === id); if (a) openCard(a.id); };
    // Grup/etiket: hücreye tıklayınca satır içinde düzenlenir (Enter kaydeder, Esc vazgeçer).
    const metaCell = (a, tr) => {
      const td = h('td', { class: 'small metacell', title: 'Grup ve etiketi değiştirmek için tıklayın' });
      const view = () => {
        tr.classList.remove('editing-row');
        td.replaceChildren(...[a.group || '—', (a.tags || []).length ? h('div', null, a.tags.map((t) => h('span', { class: 'tagchip' }, t))) : null, h('span', { class: 'editmark', 'aria-hidden': 'true' }, ' ✎')].filter(Boolean));
        td.tabIndex = 0;
      };
      const edit = () => {
        tr.classList.add('editing-row');
        td.removeAttribute('tabindex');
        const grp = h('input', { value: a.group || '', maxlength: '60', placeholder: 'Grup (ör. Üretim)', list: 'grp-list', 'aria-label': 'Grup' });
        const tgs = h('input', { value: (a.tags || []).join(', '), placeholder: 'Etiketler: web, kritik', 'aria-label': 'Etiketler (virgülle ayırın)' });
        const msg = h('span', { class: 'error small' });
        const save = async () => {
          try { await api(`/api/assets/${a.id}/meta`, { method: 'PUT', body: { group: grp.value.trim(), tags: tgs.value.split(',').map((x) => x.trim()).filter(Boolean) } }); tr.classList.remove('editing-row'); render(); }
          catch (e) { msg.textContent = e.message; }
        };
        const keys = (e) => { if (e.key === 'Enter') { e.preventDefault(); save(); } else if (e.key === 'Escape') { e.preventDefault(); view(); } };
        grp.addEventListener('keydown', keys); tgs.addEventListener('keydown', keys);
        td.replaceChildren(h('div', { class: 'stack metaedit' }, grp, tgs,
          h('div', { class: 'row' }, h('button', { type: 'button', class: 'small primary', onclick: save }, 'Kaydet'), h('button', { type: 'button', class: 'small', onclick: view }, 'Vazgeç'), msg)));
        grp.focus();
      };
      td.addEventListener('click', (e) => { if (!tr.classList.contains('editing-row')) { e.stopPropagation(); edit(); } });
      td.addEventListener('keydown', (e) => { if ((e.key === 'Enter' || e.key === ' ') && !tr.classList.contains('editing-row') && e.target === td) { e.preventDefault(); e.stopPropagation(); edit(); } });
      view();
      return td;
    };
    // Satır sonu: tür (topolojideki şekil), ayrıntı ve silme.
    const actCell = (a) => {
      const kindSel = select(null, KIND_OPTS, a.kind === 'url' ? 'website' : a.kind);
      kindSel.className = 'kindsel'; kindSel.title = 'Tür (topolojideki şekil)'; kindSel.setAttribute('aria-label', a.name + ' türü');
      kindSel.addEventListener('change', async () => {
        try { await api('/api/assets/' + a.id, { method: 'PATCH', body: { kind: kindSel.value } }); render(); } catch (e) { alert(e.message); }
      });
      return h('td', { class: 'actions acts' }, kindSel,
        h('button', { type: 'button', class: 'small', onclick: () => open(a.id) }, 'Asset kartı'),
        h('button', { type: 'button', class: 'small danger', onclick: async () => {
          if (!window.confirm(`"${a.name}" silinsin mi? Tüm metrikleri de silinir.`)) return;
          try { await api('/api/assets/' + a.id, { method: 'DELETE' }); render(); } catch (e) { alert(e.message); }
        } }, 'Sil'));
    };
    const rows = assets.map((a) => {
      const tr = h('tr', { class: 'click', 'data-id': a.id,
        'data-search': [a.name, KIND[a.kind], a.kind, STATUS[a.status], LANG[a.language], a.group, (a.tags || []).join(' ')].join(' ').toLowerCase(), 'data-status': a.status, 'data-group': a.group || '', 'data-kind': a.kind === 'url' ? 'website' : a.kind, 'data-coll': a.collection },
        h('td', null, h('span', { class: 'kl' }, kindIcon(a.kind, 30), h('b', null, a.name))),
        h('td', null, LANG[a.language]),
        h('td', null, a.collection === 'agent' ? 'Agent' : 'Agent\'sız'),
        h('td', null, pill(a.status, STATUS[a.status])));
      tr.append(metaCell(a, tr),
        h('td', { class: 'mono' }, a.latest['host.cpu_pct'] != null ? fmt(a.latest['host.cpu_pct']) + '%' : '—'),
        h('td', { class: 'mono' }, a.latest['host.mem_pct'] != null ? fmt(a.latest['host.mem_pct']) + '%' : '—'),
        h('td', { class: 'mono' }, a.latest['probe.latency_ms'] != null ? fmt(a.latest['probe.latency_ms']) + ' ms' : '—'),
        h('td', { class: 'mono small' }, ago(a.last_seen)),
        actCell(a));
      return tr;
    });
    const t = table(['Ad', 'Dil', 'Toplama', 'Durum', 'Grup / etiket', 'CPU', 'Bellek', 'Gecikme', 'Son veri', 'Tür · ayrıntı · sil'], rows,
      'Henüz asset yok. Probe\'lar sayfasından izleme ekleyin veya bir agent bağlayın.');
    t.addEventListener('click', (e) => { if (e.target.closest('td.actions, td.metacell, input, select, button')) return; const tr = e.target.closest('tr[data-id]'); if (tr) open(Number(tr.dataset.id)); });
    const cnt = (k) => assets.filter((a) => a.status === k).length;
    const cpus = assets.map((a) => a.latest['host.cpu_pct']).filter((v) => v != null);
    const avgCpu = cpus.length ? cpus.reduce((x, y) => x + y, 0) / cpus.length : null;
    const sum = h('div', { class: 'kpis' },
      kpi('Toplam asset', String(assets.length), 'izlenen', null, ''),
      kpi('Çalışıyor', String(cnt('up')), 'erişilebilir', null, ''),
      kpi('Erişilemiyor', String(cnt('down')), 'sorunlu', null, cnt('down') ? 'bad' : ''),
      kpi('Çevrimdışı', String(cnt('offline')), 'veri gelmiyor', null, cnt('offline') ? 'bad' : ''),
      kpi('Ort. CPU', avgCpu == null ? '—' : fmt(avgCpu) + '%', cpus.length + ' sunucu', null, avgCpu > 90 ? 'bad' : ''));
    const kinds = [...new Set(assets.map((a) => (a.kind === 'url' ? 'website' : a.kind)))].map((k) => [k, KIND[k] || k]);
    const fb = filterBar(FILTERS.assetler, [{ key: 'q', label: 'Ara (ad, tür, durum…)', type: 'text' },
      { key: 'status', label: 'Durum', type: 'select', options: Object.entries(STATUS) },
      { key: 'kind', label: 'Tür', type: 'select', options: kinds },
      { key: 'coll', label: 'Toplama', type: 'select', options: [['agent', 'Agent'], ['agentless', 'Agent\'sız']] },
      { key: 'group', label: 'Grup', type: 'select', options: groupsOf(assets).map((g) => [g, g]) }], t, assets.length);
    const kc = {}; assets.forEach((a) => { const k = a.kind === 'url' ? 'website' : a.kind; kc[k] = (kc[k] || 0) + 1; });
    const kEntries = Object.entries(kc).sort((a, b) => b[1] - a[1]);
    const dist = assets.length ? h('div', { class: 'donut-row' },
      donutPanel('Duruma göre', ['up', 'degraded', 'down', 'offline', 'unknown'].map((k) => ({ label: STATUS[k], value: cnt(k), cls: k })), { label: 'Asset durumu', sub: 'asset' }),
      donutPanel('Türe göre', kEntries.slice(0, 7).map(([k, n], ix) => ({ label: KIND[k] || k, value: n, cls: 'c' + (ix + 1), icon: kindIcon(k, 18) })).concat(kEntries.length > 7 ? [{ label: 'Diğer', value: kEntries.slice(7).reduce((a, x) => a + x[1], 0), cls: 'c8' }] : []), { label: 'Asset türleri', sub: 'tür' })) : null;
    const wrap = h('div', { style: null }, h('section', null, h('h2', null, 'Assetler'), sum, dist, fb, t, grpList), detail);
    // Başka sayfadan (#assetler ile) gelindiyse ayrıntı penceresini aç.
    const sel = assets.find((a) => a.id === selectedAsset);
    selectedAsset = null;
    if (sel) openCard(sel.id);
    return wrap;
  }

  function spark(points) {
    const ns = 'http://www.w3.org/2000/svg';
    const svg = document.createElementNS(ns, 'svg');
    svg.setAttribute('viewBox', '0 0 160 40');
    svg.setAttribute('preserveAspectRatio', 'none');
    svg.setAttribute('aria-hidden', 'true');
    const vals = points.map((p) => p[1]);
    const min = Math.min(...vals), max = Math.max(...vals);
    const t0 = points[0][0], t1 = points[points.length - 1][0];
    const x = (t) => (t1 === t0 ? 80 : ((t - t0) / (t1 - t0)) * 158 + 1);
    const y = (v) => (max === min ? 20 : 37 - ((v - min) / (max - min)) * 34);
    const base = document.createElementNS(ns, 'line');
    for (const [k, v] of [['x1', 0], ['x2', 160], ['y1', 39.5], ['y2', 39.5]]) base.setAttribute(k, v);
    const line = document.createElementNS(ns, 'polyline');
    line.setAttribute('points', points.map((p) => `${x(p[0]).toFixed(1)},${y(p[1]).toFixed(1)}`).join(' '));
    svg.append(base, line);
    return svg;
  }

  // Asset ayrıntısı: probe ayrıntısı gibi pencere olarak açılır (v2.7.4).
  function assetDialog(asset) {
    const body = h('div', { class: 'modal-body' }, h('p', { class: 'muted' }, 'yükleniyor…'));
    const close = openModal('Asset ayrıntısı · ' + asset.name, body, false, true);
    fillDetail(body, asset).catch((e) => body.replaceChildren(h('p', { class: 'error' }, e.message)));
    return close;
  }

  async function fillDetail(root, asset) {
    const data = await api(`/api/assets/${asset.id}/metrics?minutes=60`);
    const ovBox = h('input', { type: 'checkbox' }); ovBox.checked = asset.overview !== false;
    ovBox.addEventListener('change', async () => {
      try { await api('/api/assets/' + asset.id, { method: 'PATCH', body: { show_overview: ovBox.checked } }); asset.overview = ovBox.checked; } catch (e) { alert(e.message); ovBox.checked = !ovBox.checked; }
    });
    const names = Object.keys(data).filter((n) => !/^host\.disk_(used|total)_gb\./.test(n)).sort();
    const appsBox = h('div');
    root.replaceChildren(
      h('div', { class: 'row' }, h('h4', null, 'Son 60 dakika'),
        h('span', { class: 'row' }, names.some((n) => n.startsWith('host.')) ? h('button', { type: 'button', onclick: () => diskDialog(asset.id, asset.name) }, 'Sunucu ayrıntısı') : null,
        names.some((n) => /^host\.(cpu|mem|disk)_pct/.test(n)) ? h('label', { class: 'check' }, ovBox, 'Genel bakışta göster') : null,
        null)),
      names.length
        ? h('div', { class: 'panel sparks' }, names.map((n) => {
            const pts = data[n];
            return h('div', { class: 'spark' }, h('span', { class: 'mono small muted' }, n), spark(pts),
              h('b', { class: 'mono' }, fmt(pts[pts.length - 1][1])));
          }))
        : h('p', { class: 'muted' }, 'Son 60 dakikada metrik yok.'),
      appsBox);
    api(`/api/assets/${asset.id}/apps`).then((r) => { const el = appsSection(r); if (el) appsBox.replaceChildren(el); }).catch(() => { /* ajan uygulama listesi göndermiyor */ });
  }

  // Ajanın bildirdiği uygulamalar (süreç/servis), dinlenen portlar ve otomatik günlük özeti (v2.7.3).
  function appsSection(r) {
    if (!r || !r.apps || !r.apps.length) return null;
    const fmtMem = (mb) => (mb >= 1024 ? (mb / 1024).toFixed(1) + ' GB' : fmt(mb) + ' MB');
    const rows = r.apps.map((a) => h('tr', { 'data-search': (a.name + ' ' + a.kind + ' ' + (a.state || '') + ' ' + (a.ports || []).join(' ')).toLowerCase() },
      h('td', null, h('b', null, a.name), a.kind === 'service' ? [' ', pill('info', 'servis')] : null),
      h('td', null, a.state === 'failed' ? pill('down', 'hatalı') : a.state ? pill('up', a.state === 'running' ? 'çalışıyor' : a.state) : '—'),
      h('td', { class: 'mono' }, a.kind === 'service' ? '—' : String(a.count || 1)),
      h('td', { class: 'mono' }, a.kind === 'service' ? '—' : fmt(a.cpu_pct) + '%'),
      h('td', { class: 'mono' }, a.kind === 'service' ? '—' : fmtMem(a.mem_mb)),
      h('td', { class: 'mono small' }, (a.ports || []).join(', ') || '—')));
    const logs = (r.logs || []).length ? [h('h4', null, 'Günlükler (otomatik; yalnızca sayılar ve maskeli son hata örneği)'),
      table(['Uygulama', 'Dosya', 'Hata (1 sa)', 'Uyarı (1 sa)', 'Son hata örneği'], r.logs.map((l) => h('tr', null,
        h('td', null, h('b', null, l.app)), h('td', { class: 'mono small' }, l.path),
        h('td', { class: 'mono' }, l.readable ? (l.errors_1h ? pill('down', String(l.errors_1h)) : '0') : '—'),
        h('td', { class: 'mono' }, l.readable ? String(l.warns_1h) : '—'),
        h('td', { class: 'small' }, l.readable ? (l.sample || '—') : h('span', { class: 'muted' }, 'okunamıyor (ajanı -root ile kurun)')))), '')] : [];
    return h('section', { class: 'apps-sec' }, h('h3', null, `Uygulamalar (ajan) · ${r.apps.length}`),
      h('p', { class: 'muted small' }, `Ajanın son bildirdiği süreçler (ada göre toplanmış; CPU bir çekirdeğin yüzdesidir) ve servisler · ${ago(r.updated_at)}. En yoğun 10 uygulamanın CPU/bellek geçmişi yukarıdaki grafiklerde (proc.*) görünür.`),
      h('div', { class: 'panel stack' }, table(['Uygulama', 'Durum', 'Adet', 'CPU', 'Bellek', 'Dinlenen portlar'], rows, '', PAGE), ...logs));
  }

  function probeOpts(p) {
    const o = [p.http_method || 'GET'];
    if (p.expect_status) o.push('durum ' + p.expect_status);
    if (p.expect_text) o.push('metin aranır');
    if (p.header_names && p.header_names.length) o.push('başlık: ' + p.header_names.join(', '));
    if (p.verify_tls === false) o.push('TLS doğrulanmaz');
    if (p.follow_redirects === false) o.push('yönlendirme izlenmez');
    return o.join(' · ');
  }

  const PROBE_TYPES = [['http', 'HTTP / HTTPS'], ['tcp', 'TCP bağlantı'], ['icmp', 'ICMP (ping)'], ['snmp', 'SNMP (v1/v2c/v3)'], ['host', 'Sunucu kaynakları — CPU / RAM / Disk (SNMP veya SSH)'], ['snmpx', 'SNMP gelişmiş — arayüz trafiği, şablon, özel OID'], ['dns', 'DNS sorgusu'],
    ['tls', 'TLS sertifikası (süre)'], ['udp', 'UDP'], ['ntp', 'NTP (saat sapması)'], ['trace', 'Yol izleme (hop sayısı, rota değişimi)'], ['postgres', 'PostgreSQL (erişim)'], ['pgstats', 'PostgreSQL sağlık metrikleri (bağlantı, önbellek, kilit, boyut)']];
  const PROBE_HINTS = { http: 'https://ornek.com/health', tcp: 'sunucu.ornek.com:5432', icmp: '10.0.0.1 veya sunucu.ornek.com', snmp: '10.0.0.1 veya 10.0.0.1:161', host: 'sunucu-ip veya sunucu-ip:161', snmpx: 'cihaz-ip veya cihaz-ip:161',
    dns: 'ornek.com', tls: 'ornek.com veya ornek.com:8443', udp: 'sunucu.ornek.com:514', ntp: 'pool.ntp.org', trace: '10.0.0.1 veya ornek.com',
    postgres: 'postgres://kullanici:parola@host:5432/veritabani?sslmode=disable', pgstats: 'postgres://izleme:parola@host:5432/veritabani?sslmode=require' };
  const PROBE_HELP = { icmp: 'Paket kaybı % ve ortalama RTT ölçülür. Konteynerde ayrıcalıksız ICMP için sysctl gerekir (docker-compose.yml içinde ayarlı).',
    snmp: 'Topluluk adı ve v3 parolaları gizli saklanır, listede gösterilmez. Sayısal değer "probe.snmp_value" metriği olur.',
    host: 'Hedef sunucunun SNMP\'si açık olmalı (Linux: snmpd, Windows: SNMP Hizmeti). CPU, RAM ve tüm diskler Host Resources MIB\'den okunur; "host.cpu_pct", "host.mem_pct", "host.disk_pct" (en dolu disk) ve her disk için "host.disk_pct.<ad>" metrikleri üretilir — hazır alarm kuralları çalışır. SNMPv3 (kimlik doğrulama + gizlilik) önerilir. SSH yönteminde hedefe hiçbir şey kurulmaz: kasadaki parolasız özel anahtarla bağlanılır ve yalnızca salt-okunur bir komut çalıştırılır (CPU, RAM ve tüm bağlama noktaları /proc ve df ile okunur; ayrıca GB değerleri gelir). Sunucu anahtarı ilk bağlantıda öğrenilip sabitlenir.',
    snmpx: 'Şablon seçin (arayüzler, Linux net-snmp, Cisco, UPS) ve/veya özel OID ekleyin. Arayüzler: net.<ad>.in_bps/out_bps/util_pct/up/hata; sayaç OID\'leri "Sayaç" işaretlenirse saniyedeki artışa çevrilir. Özel değerler snmp.<ad> metriği olur.',
    trace: 'TTL artırarak hedefe giden yolu çıkarır (traceroute gibi). Metrikler: net.path.hops, net.path.rtt_ms, net.path.loss_pct ve rota değişince 1 olan net.path.changed (alarm kuralı yazılabilir). Sunucuda ham ICMP yetkisi (CAP_NET_RAW) gerekir; aradaki güvenlik duvarları ICMP\'yi engelliyorsa o sıçramalar * görünür.',
    pgstats: 'Erişimin yanı sıra db.connections, db.connections_pct, db.cache_hit_pct, db.deadlocks, db.size_mb, db.longest_query_s, db.idle_in_tx_s, db.waiting_locks ve yedek sunucuda db.repl_lag_s metriklerini üretir (alarm kuralı yazılabilir). Yalnızca okuma sorguları çalışır; salt okunur bir izleme kullanıcısı (pg_monitor rolü) önerilir. MySQL/SQL Server için sürücü yok.',
    dns: 'Çözümleme süresi ölçülür; "Beklenen" doluysa yanıtta aranır. Özel DNS sunucusu verebilirsiniz.',
    tls: 'Sertifikanın kalan gün sayısı "probe.cert_days_left" metriği olur (alarm kuralı: Sertifika süresi doluyor).',
    udp: 'UDP bağlantısızdır: yanıt gelirse başarılı sayılır. Yanıt vermeyen servisler için uygun değildir.',
    ntp: 'Sunucu saatinden sapma (ms) "probe.ntp_offset_ms" metriği olur.',
    tcp: 'İsteğe bağlı: bağlanınca metin gönderip yanıtta metin arayabilirsiniz (ör. SSH- banner\'ı, Redis PING).' };
  const SNMP_OIDS = [['1.3.6.1.2.1.1.3.0', 'sysUpTime'], ['1.3.6.1.2.1.1.1.0', 'sysDescr'], ['1.3.6.1.2.1.1.5.0', 'sysName'], ['1.3.6.1.2.1.2.1.0', 'ifNumber (arayüz sayısı)'], ['1.3.6.1.2.1.25.3.3.1.2.1', 'hrProcessorLoad (CPU %)']];

  // SNMP kimlik alanları (v1/v2c topluluk adı; v3 kullanıcı, kimlik doğrulama ve gizlilik). Parolalar gizli saklanır.
  function snmpCred(o, p) {
    o = o || {}; p = p || {};
    const sec = (ph) => h('input', { type: 'password', autocomplete: 'new-password', placeholder: ph });
    const ver = select(null, [['2c', 'v2c'], ['1', 'v1'], ['3', 'v3 (USM)']], o.snmp_version || '2c');
    const cm = sec(p.has_community ? 'değiştirmek için yazın (boş = aynı)' : 'public');
    const user = h('input', { value: o.user || '', autocomplete: 'off', maxlength: '100' });
    const ap = select(null, [['', 'Yok (noAuth)'], ['SHA', 'SHA'], ['SHA256', 'SHA-256'], ['SHA512', 'SHA-512'], ['MD5', 'MD5']], o.auth_proto || '');
    const apw = sec(p.has_authpass ? 'değiştirmek için yazın (boş = aynı)' : 'en az 8 karakter');
    const pp = select(null, [['', 'Yok'], ['AES', 'AES-128'], ['DES', 'DES']], o.priv_proto || '');
    const ppw = sec(p.has_privpass ? 'değiştirmek için yazın (boş = aynı)' : 'en az 8 karakter');
    const fc = field('Topluluk adı', cm);
    const v3 = [field('Kullanıcı adı', user), field('Kimlik doğrulama', ap), field('Doğrulama parolası', apw), field('Gizlilik (şifreleme)', pp), field('Gizlilik parolası', ppw)];
    const sync = () => { const is3 = ver.value === '3'; fc.hidden = is3; v3.forEach((e) => { e.hidden = !is3; }); };
    ver.addEventListener('change', sync); sync();
    return { els: [field('Sürüm', ver), fc, ...v3], read: () => ({ snmp_version: ver.value, community: cm.value, user: user.value.trim(), auth_proto: ap.value, auth_pass: apw.value, priv_proto: pp.value, priv_pass: ppw.value }) };
  }
  // SNMP gelişmiş: şablon, arayüz filtresi ve özel OID satırları.
  function snmpxBox(o) {
    o = o || {};
    const tpl = select(null, [['', '(şablon yok)'], ['interfaces', 'Arayüz trafiği'], ['linux', 'Linux net-snmp (CPU/RAM/yük)'], ['cisco', 'Cisco (CPU/RAM/sıcaklık)'], ['ups', 'UPS (RFC 1628)']], o.template || '');
    const flt = h('input', { value: o.if_filter || '', placeholder: 'ör. ^(eth|Gi) — boş = tümü', maxlength: '200' });
    const rows = h('div', { class: 'oidrows' });
    const addRow = (c) => {
      c = c || {};
      const nm = h('input', { value: c.name || '', placeholder: 'ad (ör. sicaklik)', maxlength: '60' });
      const od = h('input', { value: c.oid || '', placeholder: '1.3.6.1.4.1...' });
      const sc = h('input', { type: 'number', step: 'any', min: '0', value: c.scale || '', placeholder: 'çarpan' });
      const rt = h('input', { type: 'checkbox' }); rt.checked = !!c.rate;
      const del = h('button', { type: 'button', class: 'btn small' }, 'Sil');
      const row = h('div', { class: 'inline oidrow' }, nm, od, sc, h('label', { class: 'check' }, rt, 'Sayaç'), del);
      del.addEventListener('click', () => row.remove());
      row._read = () => ({ name: nm.value.trim(), oid: od.value.trim(), scale: Number(sc.value) || 0, rate: rt.checked });
      rows.append(row);
    };
    (o.oids || []).forEach(addRow);
    const add = h('button', { type: 'button', class: 'btn small' }, '+ Özel OID');
    add.addEventListener('click', () => addRow());
    const flds = [field('Şablon', tpl), field('Arayüz filtresi (regex)', flt)];
    const sync = () => { flds[1].hidden = tpl.value !== 'interfaces'; };
    tpl.addEventListener('change', sync); sync();
    return { els: [...flds, h('div', { class: 'oidwrap' }, rows, add)],
      read: () => ({ template: tpl.value, if_filter: tpl.value === 'interfaces' ? flt.value.trim() : '', oids: [...rows.children].map((r) => r._read()).filter((c) => c.name || c.oid) }) };
  }
  const HOST_METRICS = [['cpu', 'CPU'], ['mem', 'RAM'], ['disk', 'Disk']];
  function metricBoxes(sel) {
    const boxes = HOST_METRICS.map(([k, l]) => { const c = h('input', { type: 'checkbox' }); c.checked = !sel || !sel.length || sel.includes(k); return [k, c, h('label', { class: 'check' }, c, l)]; });
    return { el: h('div', { class: 'row' }, ...boxes.map((b) => b[2])), read: () => { const m = boxes.filter((b) => b[1].checked).map((b) => b[0]); return m.length === boxes.length ? [] : m; } };
  }

  // SSH ile toplanacak veriler (probe seçeneği "collect") ve kaydedilecek dizinler ("mounts").
  const SSH_DATA = [['cpu', 'CPU'], ['mem', 'RAM'], ['swap', 'Swap'], ['disk', 'Disk (dizinler)'], ['uptime', 'Çalışma süresi'], ['load', 'Yük ortalaması'],
    ['conns', 'Açık bağlantılar'], ['listen', 'Dinleyen portlar (saldırı yüzeyi)'], ['addr', 'IP adresleri']];
  function sshDataBox(o) {
    o = o || {};
    let sel = o.collect && o.collect.length ? o.collect : null;
    if (!sel) { // eski probe: cpu/mem/disk "metrics" seçimine uyar; uptime, bağlantı, port ve adres her zaman toplanıyordu
      const m = o.metrics && o.metrics.length ? o.metrics : ['cpu', 'mem', 'disk'];
      sel = [...m, 'uptime', 'conns', 'listen', 'addr'];
    }
    const boxes = SSH_DATA.map(([k, l]) => { const c = h('input', { type: 'checkbox' }); c.checked = sel.includes(k); return [k, c, h('label', { class: 'check' }, c, l)]; });
    boxes.forEach(([, c]) => c.addEventListener('change', () => { if (!boxes.some((b) => b[1].checked)) c.checked = true; })); // en az bir veri
    const mounts = h('input', { value: (o.mounts || []).join(', '), maxlength: '600', placeholder: 'boş = tüm dizinler; ör. /, /var, /data', autocomplete: 'off' });
    const grid = h('div', { class: 'row' }, ...boxes.map((b) => b[2]));
    grid.style.flexWrap = 'wrap'; grid.style.justifyContent = 'flex-start'; grid.style.gap = '6px 18px';
    const f1 = field('Toplanacak veriler (SSH)', grid), f2 = field('Kaydedilecek dizinler (virgülle ayırın)', mounts);
    f1.style.gridColumn = '1 / -1'; f2.style.gridColumn = 'span 2';
    const els = [f1, f2];
    return { els, read: () => ({ collect: boxes.filter((b) => b[1].checked).map((b) => b[0]), mounts: mounts.value.split(',').map((x) => x.trim()).filter(Boolean), metrics: [] }) };
  }

  // SSH yöntemi: kullanıcı, kasadaki anahtar ve (varsa) sabitlenmiş sunucu parmak izi.
  let SECRET_NAMES = {};
  function sshBox(o, p, secrets, cred, metricEl) {
    o = o || {}; p = p || {};
    const keys = (secrets || []).filter((x) => x.kind === 'ssh_key' || x.kind === 'password');
    const method = select(null, [['snmp', 'SNMP (agent\'sız, hedefte snmpd gerekir)'], ['ssh', 'SSH (kasadaki anahtar/parola ile, hedefe kurulum gerekmez)']], o.method === 'ssh' ? 'ssh' : 'snmp');
    const user = h('input', { value: o.ssh_user || '', autocomplete: 'off', maxlength: '32', placeholder: 'ör. appmon' });
    const key = select(null, [['', keys.length ? '(anahtar seçin)' : '(kasada SSH anahtarı yok — Kasa sayfasından ekleyin)'], ...keys.map((k) => [String(k.id), k.name])], o.secret_id ? String(o.secret_id) : '');
    const reset = h('input', { type: 'checkbox' });
    const legacy = h('input', { type: 'checkbox' });
    legacy.checked = !!o.ssh_legacy;
    const fp = p.host_fp
      ? h('div', { class: 'small' }, h('span', { class: 'muted' }, 'Sabitlenmiş sunucu anahtarı: '), h('span', { class: 'mono' }, p.host_fp),
        h('label', { class: 'check' }, reset, 'Parmak izini sıfırla (sunucu yeniden kurulduysa; sonraki bağlantıda yeniden öğrenilir)'))
      : h('div', { class: 'small muted' }, 'Sunucu anahtarı ilk başarılı bağlantıda öğrenilip sabitlenir (parmak izi burada görünür).');
    const lg = h('label', { class: 'check small', title: 'Eski sshd (RHEL 5/6, eski ağ cihazları) için zayıf algoritmaları ek olarak açar: diffie-hellman-group1/14-sha1, ssh-rsa/ssh-dss, CBC şifreleri, hmac-sha1' }, legacy, 'Eski SSH sunucusu uyumluluğu (zayıf algoritmalara izin ver; yalnızca eski cihazlar için)');
    const data = sshDataBox(o);
    const sshEls = [field('SSH kullanıcısı', user), field('Kimlik bilgisi (kasa: anahtar veya parola)', key), lg, ...data.els, fp];
    const show = (e, on) => { e.style.display = on ? '' : 'none'; };
    const sync = () => {
      const ssh = method.value === 'ssh';
      sshEls.forEach((e) => show(e, ssh));
      cred.els.forEach((e) => show(e, !ssh));
      if (metricEl) show(metricEl, !ssh);
    };
    method.addEventListener('change', sync); sync();
    return { methodEl: field('Yöntem', method), sshEls, sync, isSSH: () => method.value === 'ssh',
      read: () => ({ method: 'ssh', ssh_user: user.value.trim(), secret_id: Number(key.value) || 0, host_key: reset.checked ? '' : (o.host_key || ''), ssh_legacy: legacy.checked, ...data.read() }) };
  }

  function optSummary(p) {
    const o = p.options || {}, r = [];
    if (o.count) r.push(o.count + ' paket');
    if (p.type === 'snmp') r.push('OID ' + (o.oid || '1.3.6.1.2.1.1.3.0'));
    if (p.type === 'snmpx') r.push((o.template ? 'şablon ' + o.template : 'özel OID') + ((o.oids || []).length ? ' +' + o.oids.length + ' OID' : ''));
    const isSSH = p.type === 'host' && o.method === 'ssh';
    if (isSSH) r.push('SSH ' + (o.ssh_user || '?') + '@', 'kasa: ' + (SECRET_NAMES[o.secret_id] || '#' + o.secret_id), p.host_fp ? 'sunucu anahtarı sabit' : 'sunucu anahtarı bekleniyor');
    if (isSSH && o.ssh_legacy) r.push('eski SSH uyumluluğu');
    if (!isSSH && (p.type === 'snmp' || p.type === 'host' || p.type === 'snmpx')) {
      const v = o.snmp_version || '2c';
      r.push('SNMP v' + v);
      if (v === '3') r.push('kullanıcı ' + (o.user || '?'), o.auth_proto ? (o.auth_proto + (o.priv_proto ? '/' + o.priv_proto : '')) : 'doğrulamasız');
      else r.push(p.has_community ? 'topluluk gizli' : 'topluluk: public');
    }
    if (p.type === 'host') {
      if (isSSH && o.collect && o.collect.length) { const nm = Object.fromEntries(SSH_DATA); r.push(o.collect.map((m) => nm[m] || m).join('/')); if (o.mounts && o.mounts.length) r.push('dizin: ' + o.mounts.join(', ')); }
      else r.push(!o.metrics || !o.metrics.length ? 'CPU/RAM/Disk' : o.metrics.map((m) => ({ cpu: 'CPU', mem: 'RAM', disk: 'Disk' }[m])).join('/'));
    }
    if (o.record) r.push(o.record + ' kaydı');
    if (o.server) r.push('sunucu ' + o.server);
    if (o.send) r.push('gönderir');
    if (o.expect) r.push('metin aranır');
    if (p.type === 'tls' && p.verify_tls === false) r.push('doğrulama kapalı');
    return r.join(' · ');
  }

  async function viewProbelar() {
    await refreshHeader();
    const probes = await api('/api/probes');
    const secrets = canWrite() ? await api('/api/secrets').catch(() => []) : [];
    SECRET_NAMES = Object.fromEntries(secrets.map((x) => [x.id, x.name]));
    const msg = h('p', { class: 'msg', role: 'status' });
    const f = h('form', { class: 'inline', id: 'probe-form' },
      field('Asset adı', h('input', { id: 'p-asset', required: true, maxlength: '200', placeholder: 'ör. odeme-servisi' })),
      field('Dil (isteğe bağlı)', select('p-lang', Object.entries(LANG).map(([k, v]) => [k, k === 'none' ? 'Belirtilmedi' : v]))),
      field('Probe türü', select('p-type', PROBE_TYPES.map(([k, l]) => [k, l]))),
      field('Asset türü (topoloji şekli)', select('p-kind', KIND_AUTO)),
      h('label', { class: 'check', id: 'p-ovwrap', hidden: true }, h('input', { type: 'checkbox', id: 'p-ov', checked: true }), 'Bu makinenin CPU / RAM / Disk değerlerini Genel bakışta göster'),
      field('Hedef', h('input', { id: 'p-target', required: true, placeholder: 'https://ornek.com/health' })),
      field('Aralık (sn)', h('input', { id: 'p-interval', type: 'number', min: '5', value: '30' })),
      field('Zaman aşımı (ms)', h('input', { id: 'p-timeout', type: 'number', min: '100', max: '60000', value: '5000' })),
      h('button', { type: 'submit', class: 'primary' }, 'Probe ekle'));
    const adv = h('details', { class: 'adv', open: false },
      h('summary', null, 'Gelişmiş HTTP seçenekleri'),
      h('div', { class: 'inline' },
        field('Yöntem', select('p-method', METHODS.map((m) => [m, m]))),
        field('Beklenen durum kodu', h('input', { id: 'p-status', placeholder: 'boş = 200-399; ör. 200 veya 200,204,301-302' })),
        field('Yanıtta aranacak metin', h('input', { id: 'p-text', maxlength: '200', placeholder: 'ör. "status":"ok"' })),
        field('Başlıklar (satır başına "Ad: değer")', area('p-headers', 3, 'Authorization: Bearer ...')),
        field('Gövde (POST/PUT/PATCH)', area('p-body', 3, '{"ping":true}')),
        h('label', { class: 'check' }, h('input', { id: 'p-tls', type: 'checkbox', checked: true }), 'TLS sertifikasını doğrula'),
        h('label', { class: 'check' }, h('input', { id: 'p-redir', type: 'checkbox', checked: true }), 'Yönlendirmeleri izle')));
    f.insertBefore(adv, f.lastChild);
    adv.style.gridColumn = '1 / -1';
    const cred = snmpCred(), metrics = metricBoxes(), sx = snmpxBox(), ssh = sshBox({}, {}, secrets, cred, metrics.el);
    // Türe özgü seçenekler: her alan hangi türlerde görüneceğini 'types' ile belirtir.
    const optFields = [
      ['icmp', field('Paket sayısı', h('input', { id: 'o-count', type: 'number', min: '1', max: '10', value: '3' }))],
      ['host', ssh.methodEl, ...ssh.sshEls],
      ['snmp host snmpx', ...cred.els],
      ['snmpx', ...sx.els],
      ['host', metrics.el],
      ['snmp', field('OID', h('input', { id: 'o-oid', list: 'o-oids', placeholder: '1.3.6.1.2.1.1.3.0' })), h('datalist', { id: 'o-oids' }, SNMP_OIDS.map(([v, l]) => h('option', { value: v }, l)))],
      ['dns', field('Kayıt türü', select('o-rec', ['A', 'AAAA', 'MX', 'TXT', 'CNAME', 'NS'].map((x) => [x, x])))],
      ['dns', field('DNS sunucusu (isteğe bağlı)', h('input', { id: 'o-srv', placeholder: '8.8.8.8 veya 10.0.0.53:53' }))],
      ['trace', field('Paket işareti (DSCP)', select('o-dscp', DSCP_CHOICES.map(([k, l]) => [String(k), l])))],
      ['tcp udp', field('Gönderilecek metin (\\r \\n kullanılabilir)', h('input', { id: 'o-send', maxlength: '1024', placeholder: 'PING\\r\\n' }))],
      ['tcp udp snmp dns', field('Yanıtta aranacak metin', h('input', { id: 'o-expect', maxlength: '200', placeholder: '+PONG' }))],
    ];
    const optBox = h('div', { class: 'inline', style: null });
    optBox.style.gridColumn = '1 / -1';
    const optItems = optFields.map(([types, ...els]) => ({ types: types.split(' '), els }));
    optItems.forEach((it) => it.els.forEach((e) => optBox.append(e)));
    const help = h('p', { class: 'muted small' });
    help.style.gridColumn = '1 / -1';
    const tlsBox = h('label', { class: 'check' }, h('input', { id: 'o-tlsverify', type: 'checkbox', checked: true }), 'Sertifika zincirini doğrula (kapalıysa yalnızca kalan gün ölçülür)');
    f.insertBefore(optBox, adv); f.insertBefore(help, adv); f.insertBefore(tlsBox, adv);
    const syncType = () => {
      const t = $('#p-type', f).value;
      $('#p-target', f).placeholder = PROBE_HINTS[t];
      adv.hidden = t !== 'http';
      for (const it of optItems) it.els.forEach((e) => { e.hidden = !it.types.includes(t); });
      if (t === 'host') ssh.sync();
      optBox.hidden = !optItems.some((it) => it.types.includes(t));
      tlsBox.hidden = t !== 'tls';
      help.textContent = PROBE_HELP[t] || ''; help.hidden = !PROBE_HELP[t];
      $('#p-ovwrap', f).hidden = t !== 'host';
      const slow = t === 'host' || t === 'snmpx', tmo = $('#p-timeout', f); if (slow && tmo.value === '5000') tmo.value = '15000'; else if (!slow && tmo.value === '15000') tmo.value = '5000';
    };
    $('#p-type', f).addEventListener('change', syncType);
    syncType();
    f.addEventListener('submit', async (e) => {
      e.preventDefault();
      msg.className = 'msg'; msg.textContent = '';
      try {
        const body = {
          asset_name: $('#p-asset', f).value, language: $('#p-lang', f).value, type: $('#p-type', f).value,
          target: $('#p-target', f).value, interval_sec: Number($('#p-interval', f).value), timeout_ms: Number($('#p-timeout', f).value) };
        if ($('#p-kind', f).value) body.asset_kind = $('#p-kind', f).value;
        if (body.type === 'http') {
          Object.assign(body, {
            http_method: $('#p-method', f).value, expect_status: $('#p-status', f).value, expect_text: $('#p-text', f).value,
            headers: parseHeaders($('#p-headers', f).value), body: $('#p-body', f).value,
            verify_tls: $('#p-tls', f).checked, follow_redirects: $('#p-redir', f).checked });
        }
        const t = body.type, o = {}, val = (id) => $(id, f).value.trim();
        if (t === 'icmp') o.count = Number(val('#o-count')) || 3;
        if (t === 'snmp' || t === 'snmpx' || (t === 'host' && !ssh.isSSH())) Object.assign(o, cred.read());
        if (t === 'snmpx') Object.assign(o, sx.read());
        if (t === 'snmp') o.oid = val('#o-oid');
        if (t === 'host') { if (ssh.isSSH()) Object.assign(o, ssh.read(), { host_key: '' }); else { o.method = 'snmp'; o.metrics = metrics.read(); } }
        if (t === 'dns') Object.assign(o, { record: val('#o-rec'), server: val('#o-srv') });
        if (t === 'trace') o.dscp = Number(val('#o-dscp') || 0);
        if (t === 'tcp' || t === 'udp') o.send = $('#o-send', f).value;
        if (['tcp', 'udp', 'snmp', 'dns'].includes(t)) o.expect = $('#o-expect', f).value;
        if (t === 'tls') body.verify_tls = $('#o-tlsverify', f).checked;
        if (t === 'host') body.show_overview = $('#p-ov', f).checked;
        body.options = o;
        await api('/api/probes', { method: 'POST', body });
        render();
      } catch (err) { msg.className = 'msg error'; msg.textContent = err.message; }
    });
    const editBox = h('div');
    const editProbe = (p) => {
      const o = p.options || {}, emsg = h('p', { class: 'msg', role: 'status' });
      const inp = (v, extra) => h('input', { value: v == null ? '' : String(v), ...(extra || {}) });
      const target = inp(p.target, { required: true, maxlength: '1000' }), iv = inp(p.interval_sec, { type: 'number', min: '5' }), to = inp(p.timeout_ms, { type: 'number', min: '100', max: '60000' });
      const en = h('input', { type: 'checkbox' }); en.checked = p.enabled;
      const extra = []; let collect = () => ({});
      if (p.type === 'http') {
        const m = select(null, ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE'].map((x) => [x, x]), p.http_method || 'GET');
        const st = inp(p.expect_status, { placeholder: 'ör. 200 veya 200-299' }), tx = inp(p.expect_text, { maxlength: '200' });
        const hd = h('textarea', { rows: '3', placeholder: (p.header_names && p.header_names.length ? 'Mevcut: ' + p.header_names.join(', ') + ' (değerler gizli). ' : '') + 'Boş bırakırsanız başlıklar aynı kalır; yazarsanız hepsinin yerini alır. Her satır: Ad: değer', spellcheck: 'false' });
        const bd = h('textarea', { rows: '3', placeholder: 'Boş bırakırsanız gövde aynı kalır', spellcheck: 'false' });
        const tls = h('input', { type: 'checkbox' }); tls.checked = p.verify_tls !== false;
        const fo = h('input', { type: 'checkbox' }); fo.checked = p.follow_redirects !== false;
        extra.push(h('div', { class: 'inline' }, field('Yöntem', m), field('Beklenen durum', st), field('Beklenen metin', tx)), field('Ek başlıklar', hd), field('Gövde', bd),
          h('div', { class: 'row' }, h('label', { class: 'check' }, tls, 'TLS sertifikasını doğrula'), h('label', { class: 'check' }, fo, 'Yönlendirmeleri izle')));
        collect = () => { const r = { http_method: m.value, expect_status: st.value, expect_text: tx.value, verify_tls: tls.checked, follow_redirects: fo.checked };
          if (hd.value.trim()) r.headers = parseHeaders(hd.value); if (bd.value) r.body = bd.value; return r; };
      } else if (p.type === 'icmp') { const c = inp(o.count || 3, { type: 'number', min: '1', max: '10' }); extra.push(field('Paket sayısı', c)); collect = () => ({ options: { count: Number(c.value) || 3 } }); }
      else if (p.type === 'snmp') {
        const cr = snmpCred(o, p), oid = inp(o.oid, { list: 'pe-oids' }), ex = inp(o.expect);
        extra.push(h('datalist', { id: 'pe-oids' }, SNMP_OIDS.map(([v, t]) => h('option', { value: v }, t))), h('div', { class: 'inline' }, ...cr.els, field('OID', oid), field('Yanıtta aranacak metin', ex)));
        collect = () => ({ options: { ...cr.read(), oid: oid.value.trim(), expect: ex.value } });
      } else if (p.type === 'host') {
        const cr = snmpCred(o, p), mb = metricBoxes(o.metrics), mbf = field('Ölçümler', mb.el), sb = sshBox(o, p, secrets, cr, mbf);
        extra.push(h('div', { class: 'inline' }, sb.methodEl, ...sb.sshEls, ...cr.els), mbf);
        collect = () => ({ options: sb.isSSH() ? sb.read() : { ...cr.read(), method: 'snmp', metrics: mb.read() } });
      } else if (p.type === 'snmpx') {
        const cr = snmpCred(o, p), xb = snmpxBox(o);
        extra.push(h('div', { class: 'inline' }, ...cr.els, ...xb.els));
        collect = () => ({ options: { ...cr.read(), ...xb.read() } });
      } else if (p.type === 'dns') {
        const rec = select(null, ['A', 'AAAA', 'MX', 'TXT', 'CNAME', 'NS'].map((x) => [x, x]), o.record || 'A'), sv = inp(o.server, { placeholder: 'isteğe bağlı' }), ex = inp(o.expect);
        extra.push(h('div', { class: 'inline' }, field('Kayıt türü', rec), field('DNS sunucusu', sv), field('Yanıtta aranacak metin', ex)));
        collect = () => ({ options: { record: rec.value, server: sv.value.trim(), expect: ex.value } });
      } else if (p.type === 'trace') {
        const ds = select(null, DSCP_CHOICES.map(([k, l]) => [String(k), l]), String(o.dscp || 0));
        extra.push(h('div', { class: 'inline' }, field('Paket işareti (DSCP)', ds)));
        collect = () => ({ options: { dscp: Number(ds.value) } });
      } else if (p.type === 'tcp' || p.type === 'udp') {
        const sd = inp(o.send), ex = inp(o.expect);
        extra.push(h('div', { class: 'inline' }, field('Gönderilecek metin', sd), field('Yanıtta aranacak metin', ex)));
        collect = () => ({ options: { send: sd.value, expect: ex.value } });
      } else if (p.type === 'tls') {
        const v = h('input', { type: 'checkbox' }); v.checked = p.verify_tls !== false;
        extra.push(h('label', { class: 'check' }, v, 'Sertifika zincirini doğrula')); collect = () => ({ verify_tls: v.checked });
      }
      const save = h('button', { type: 'submit', class: 'primary' }, 'Kaydet');
      const form = h('form', { class: 'stack' },
        h('p', { class: 'muted small' }, `Tür: ${p.type.toUpperCase()} · Asset: ${p.asset} (tür ve asset değiştirilemez; gerekirse silip yeniden ekleyin)`),
        h('div', { class: 'inline' }, field('Hedef', target), field('Aralık (sn)', iv), field('Zaman aşımı (ms)', to)),
        h('label', { class: 'check' }, en, 'Etkin'), ...extra,
        h('div', { class: 'row' }, save, h('button', { type: 'button', onclick: () => editBox.replaceChildren() }, 'Vazgeç')), emsg);
      form.addEventListener('submit', async (e) => {
        e.preventDefault(); save.disabled = true; emsg.className = 'msg'; emsg.textContent = 'Kaydediliyor…';
        try { await api('/api/probes/' + p.id, { method: 'PUT', body: { target: target.value, interval_sec: Number(iv.value), timeout_ms: Number(to.value), enabled: en.checked, ...collect() } }); render(); }
        catch (x) { emsg.className = 'msg error'; emsg.textContent = x.message; save.disabled = false; }
      });
      editBox.replaceChildren(h('section', null, h('h3', null, 'Probe\'u değiştir: ' + p.asset), h('div', { class: 'panel' }, form)));
      editBox.scrollIntoView({ block: 'start', behavior: 'smooth' });
    };
    const rows = probes.map((p) => h('tr', { 'data-search': [p.asset, p.type, p.target, p.last_error].join(' ').toLowerCase(), 'data-type': p.type, 'data-res': !p.last_run ? 'wait' : p.last_error ? 'fail' : 'ok' },
      h('td', null, h('b', null, p.asset)), h('td', null, p.type.toUpperCase()),
      h('td', { class: 'mono small' }, p.target, p.type === 'http' ? h('div', { class: 'muted' }, probeOpts(p)) : p.options ? h('div', { class: 'muted' }, optSummary(p)) : null), h('td', { class: 'mono' }, p.interval_sec + ' sn'),
      h('td', { class: 'mono small' }, ago(p.last_run)),
      h('td', null, !p.last_run ? pill('unknown', 'Bekliyor') : p.last_error ? h('span', { class: 'error small' }, p.last_error) : pill('up', 'Başarılı')),
      h('td', null, h('span', { class: 'row' }, h('button', { type: 'button', onclick: () => probeDialog(p.id, p.asset) }, 'Ayrıntı'), h('button', { type: 'button', onclick: () => editProbe(p) }, 'Değiştir'), h('button', { type: 'button', class: 'danger', onclick: async () => { try { await api('/api/probes/' + p.id, { method: 'DELETE' }); render(); } catch (e) { alert(e.message); } } }, 'Sil')))));
    const types = {}; probes.forEach((p) => { types[p.type] = (types[p.type] || 0) + 1; });
    const tEntries = Object.entries(types).sort((a, b) => b[1] - a[1]);
    const pOk = probes.filter((p) => p.last_run && !p.last_error).length, pBad = probes.filter((p) => p.last_run && p.last_error).length, pWait = probes.length - pOk - pBad;
    const pSum = h('div', { class: 'donut-row' },
      donutPanel('Probe türleri', tEntries.slice(0, 7).map(([k, n], ix) => ({ label: k.toUpperCase(), value: n, cls: 'c' + (ix + 1), go: () => { FILTERS.probelar.type = k; render(); } })).concat(tEntries.length > 7 ? [{ label: 'Diğer', value: tEntries.slice(7).reduce((a, x) => a + x[1], 0), cls: 'c8' }] : []), { label: 'Probe türleri', sub: 'probe' }),
      donutPanel('Son sonuç', [{ label: 'Başarılı', value: pOk, cls: 'up' }, { label: 'Hatalı', value: pBad, cls: 'down' }, { label: 'Bekliyor', value: pWait, cls: 'wait' }], { label: 'Probe sonuçları', sub: 'probe' }));
    return h('div', { style: null },
      h('section', null, h('h2', null, 'Agent\'sız izleme (probe)'), probes.length ? pSum : null,
        h('p', { class: 'muted' }, 'Sunucunuza hiçbir şey kurmadan HTTP, TCP, ICMP (ping), SNMP, DNS, TLS sertifikası, UDP, NTP ve PostgreSQL erişilebilirliğini ve gecikmesini izler. Yeni bir asset adı yazarsanız lisanstan bir asset düşer.'),
        h('p', { class: 'muted' }, 'APP_MON Docker içinde çalışıyorsa localhost konteynerin kendisidir. Windows veya Mac\'teki bir uygulamayı izlemek için localhost yerine host.docker.internal yazın (ör. host.docker.internal:43130).'),
        h('div', { class: 'panel' }, f, msg), canWrite() ? h('div', { class: 'row' }, h('button', { type: 'button', onclick: probeImportDialog }, 'CSV ile toplu içe aktar…')) : null),
      h('section', null, (() => {
        const sp = canWrite() && probes.length ? selectable(rows, probes.map((p) => p.id), 'probe') : null;
        const t = table((sp ? [''] : []).concat(['Asset', 'Tür', 'Hedef', 'Aralık', 'Son çalışma', 'Son sonuç', '']), rows, 'Henüz probe tanımlanmadı.');
        const fb = filterBar(FILTERS.probelar, [{ key: 'q', label: 'Ara (asset, hedef, hata…)', type: 'text' },
          { key: 'type', label: 'Tür', type: 'select', options: [...new Set(probes.map((p) => p.type))].map((x) => [x, x.toUpperCase()]) },
          { key: 'res', label: 'Sonuç', type: 'select', options: [['ok', 'Başarılı'], ['fail', 'Hatalı'], ['wait', 'Bekliyor']] }], t, probes.length);
        const bulk = sp ? sp.bar([bulkBtn('Etkinleştir', '/api/probes/bulk', sp.sel, { action: 'enable' }), bulkBtn('Durdur', '/api/probes/bulk', sp.sel, { action: 'disable' }),
          bulkSetButton('/api/probes/bulk', sp.sel, 5, true),
          bulkBtn('Sil', '/api/probes/bulk', sp.sel, { action: 'delete' }, '{n} probe silinsin mi? Bu işlem geri alınamaz.', 'danger')]) : null;
        return h('div', { class: 'stack' }, fb, bulk, t);
      })()),
      editBox);
  }


  function stepCard(n, onRemove) {
    const c = h('fieldset', { class: 'step' },
      h('legend', null, 'Adım ' + n),
      h('div', { class: 'inline' },
        field('Ad', h('input', { class: 's-name', maxlength: '100', placeholder: 'ör. Giriş' })),
        field('Yöntem', h('select', { class: 's-method' }, METHODS.map((m) => h('option', { value: m }, m)))),
        field('Adres', h('input', { class: 's-url', placeholder: 'http://host.docker.internal:43130/api/login' })),
        field('Beklenen durum', h('input', { class: 's-status', placeholder: '200' })),
        field('Yanıtta aranacak metin', h('input', { class: 's-text', maxlength: '200' })),
        field('En fazla süre (ms)', h('input', { class: 's-max', type: 'number', min: '0', placeholder: 'sınırsız' }))),
      h('div', { class: 'inline' },
        field('Başlıklar ("Ad: değer")', h('textarea', { class: 's-headers', rows: '3', spellcheck: 'false', placeholder: 'Authorization: Bearer {{token}}' })),
        field('Gövde', h('textarea', { class: 's-body', rows: '3', spellcheck: 'false', placeholder: '{"kullanici":"{{kullanici}}","parola":"{{parola}}"}' })),
        field('Yanıttan değer al ("ad = json:yol")', h('textarea', { class: 's-extract', rows: '3', spellcheck: 'false', placeholder: 'token = json:data.token\noturum = header:X-Request-Id\nurun = regex:ürün: (\\w+)' }))),
      h('button', { type: 'button', class: 'danger', onclick: onRemove }, 'Adımı kaldır'));
    return c;
  }

  function readSteps(box) {
    return [...box.querySelectorAll('.step')].map((c, i) => {
      const v = (sel) => $(sel, c).value;
      const st = { name: v('.s-name').trim() || 'Adım ' + (i + 1), method: v('.s-method'), url: v('.s-url').trim() };
      const hd = parseHeaders(v('.s-headers')); if (Object.keys(hd).length) st.headers = hd;
      if (v('.s-body')) st.body = v('.s-body');
      if (v('.s-status').trim()) st.expect_status = v('.s-status').trim();
      if (v('.s-text')) st.expect_text = v('.s-text');
      if (Number(v('.s-max')) > 0) st.max_ms = Number(v('.s-max'));
      const ex = parseExtract(v('.s-extract')); if (ex.length) st.extract = ex;
      return st;
    });
  }

  function stepResults(res) {
    return h('div', { class: 'panel' },
      h('p', null, res.ok ? pill('up', 'Başarılı') : pill('down', 'Başarısız'), ' ', h('span', { class: 'mono small' }, fmt(res.duration_ms) + ' ms')),
      res.error ? h('p', { class: 'error small' }, res.error) : null,
      table(['Adım', 'Durum / sayfa', 'Süre', 'Sonuç'], (res.steps || []).map((r) => h('tr', null,
        h('td', null, r.name), h('td', { class: 'mono small' }, r.status || r.page || '—'), h('td', { class: 'mono' }, fmt(r.ms) + ' ms'),
        h('td', null, r.ok ? pill('up', 'Tamam') : h('span', { class: 'error small' }, r.error || 'Hata')))), 'Adım çalışmadı.'));
  }


  // ---- Gömülü tarayıcı kaydedici ----
  let recCleanup = null;
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const SPECIAL = new Set(['Enter', 'Tab', 'Escape', 'Backspace', 'Delete', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End', 'PageUp', 'PageDown']);

  function recorderPanel(assets, probesAll) {
    const root = h('div', { class: 'stack' });
    const msg = h('p', { class: 'msg', role: 'status' });
    const out = h('div');

    let preAuth = null;
    function loginReport(r) {
      const ROLE = { username: 'Kullanıcı adı / e-posta', password: 'Parola', otp: 'Tek kullanımlık doğrulama kodu', captcha: 'Captcha', select: 'Seçim listesi', checkbox: 'Onay kutusu', other: 'Ek alan' };
      const items = [], warns = [];
      if (r.logged_in) warns.push('Bu adreste giriş formu bulunamadı: sayfa giriş gerektirmiyor, zaten giriş yapılmış ya da giriş ekranı farklı bir adreste olabilir. Giriş sayfasının adresini yazın.');
      for (const f of r.fields) {
        const base = `${ROLE[f.role] || f.role}: “${f.label || f.name || f.type}”${f.required ? ' (zorunlu)' : ''}`;
        if (f.role === 'username') items.push(base + ' → yukarıdaki “Kullanıcı adı” kutusuna yazın.');
        else if (f.role === 'password') items.push(base + ' → yukarıdaki “Parola” kutusuna yazın.');
        else if (f.role === 'otp') warns.push(base + ': iki adımlı doğrulama isteniyor. APP_MON kod giremez; otomatik giriş çalışmaz. İzleme için 2 adımlı doğrulaması kapalı, yetkisi sınırlı bir test kullanıcısı isteyin.');
        else if (f.role === 'captcha') warns.push(base + ': captcha otomatik geçilemez. İzleme için captcha’sız bir test kullanıcısı/adresi (veya güvenilir IP muafiyeti) isteyin.');
        else if (f.role === 'select') items.push(base + (f.options && f.options.length ? ` (seçenekler: ${f.options.join(', ')})` : '') + ' → APP_MON bu alanı doldurmaz, varsayılan seçenek kalır. Giriş için belirli bir seçim gerekiyorsa “Kayıttaki adımlar giriş olarak çalışsın” yöntemini kullanın.');
        else if (f.role === 'checkbox') items.push(base + ' → APP_MON işaretlemez (ör. “beni hatırla” gerekmez).');
        else items.push(base + ' → APP_MON yalnızca kullanıcı adı ve parolayı doldurur; bu alan zorunluysa (ör. şirket/firma kodu) otomatik giriş başarısız olur. Bu durumda “Kayıttaki adımlar giriş olarak çalışsın” yöntemini kullanın.');
      }
      if (r.captcha && !r.fields.some((f) => f.role === 'captcha')) warns.push('Sayfada captcha (robot doğrulaması) görünüyor: otomatik giriş çalışmayabilir.');
      if (r.sso && r.sso.length) warns.push('Harici giriş düğmeleri var (' + r.sso.join(', ') + '). Bunlar otomatik doldurulamaz; kullanıcı adı/parola formu da varsa onu kullanın.');
      if (r.no_password && !r.logged_in) warns.push('Parola alanı görünmüyor: önce kullanıcı adı, sonra parola isteyen iki adımlı bir giriş ekranı olabilir. Bu durumda “Kayıttaki adımlar giriş olarak çalışsın” yöntemi daha uygundur.');
      const need = r.fields.filter((f) => f.role === 'username' || f.role === 'password').map((f) => ROLE[f.role]);
      return h('div', { class: 'panel stack' },
        h('b', null, 'Giriş ekranı analizi' + (r.title ? ': ' + r.title : '')),
        need.length ? h('p', null, 'Girmeniz gerekenler: ', h('b', null, need.join(' ve ')), r.submit ? ` · Giriş düğmesi: “${r.submit}”` : '') : null,
        items.length ? h('ul', { class: 'small' }, items.map((t) => h('li', null, t))) : null,
        warns.length ? h('div', { class: 'msg error' }, h('b', null, 'Dikkat'), h('ul', { class: 'small' }, warns.map((t) => h('li', null, t)))) : null,
        !warns.length && need.length ? h('p', { class: 'msg ok small' }, 'Standart bir kullanıcı adı/parola formu: otomatik giriş çalışmalı. Alan seçicileri bulunduysa aşağıda önceden dolduruldu.') : null,
        h('p', { class: 'muted small' }, 'Çözümlenen adres: ' + r.url));
    }
    function idle() {
      preAuth = null;
      const url = h('input', { id: 'rec-url', placeholder: 'http://host.docker.internal:43130/', value: '' });
      const start = h('button', { type: 'submit', class: 'primary' }, 'Kaydı başlat');
      const aOn = h('input', { id: 'ra-on', type: 'checkbox' });
      const aUser = h('input', { id: 'ra-user', autocomplete: 'off', maxlength: '300' });
      const aPass = h('input', { id: 'ra-pass', type: 'password', autocomplete: 'new-password', maxlength: '300' });
      const aText = h('input', { id: 'ra-text', maxlength: '200', placeholder: 'ör. Çıkış yap' });
      const aSel = ['ra-usel', 'ra-psel', 'ra-ssel'].map((id, i) => h('input', { id, placeholder: ['ör. #email', 'ör. input[name=pw]', 'ör. button[type=submit]'][i] }));
      const anaOut = h('div', { class: 'stack' });
      const anaBtn = h('button', { type: 'button' }, 'Giriş ekranını analiz et');
      anaBtn.addEventListener('click', async () => {
        if (!url.value.trim()) { anaOut.replaceChildren(h('p', { class: 'msg error' }, 'Önce başlangıç adresini (giriş sayfası) yazın.')); return; }
        anaBtn.disabled = true; anaOut.replaceChildren(h('p', { class: 'muted small' }, 'Sayfa açılıyor ve çözümleniyor…'));
        try {
          const r = await api('/api/recorder/analyze-login', { method: 'POST', body: { url: url.value.trim() } });
          anaOut.replaceChildren(loginReport(r));
          const u = r.fields.find((x) => x.role === 'username' && x.selector), p = r.fields.find((x) => x.role === 'password' && x.selector);
          if (u && !aSel[0].value) aSel[0].value = u.selector;
          if (p && !aSel[1].value) aSel[1].value = p.selector;
          if (r.submit_selector && !aSel[2].value) aSel[2].value = r.submit_selector;
        } catch (err) { anaOut.replaceChildren(h('p', { class: 'msg error' }, err.message)); }
        anaBtn.disabled = false;
      });
      const aBody = h('div', { class: 'stack', hidden: true },
        h('div', { class: 'row' }, anaBtn, h('span', { class: 'muted small' }, 'Giriş sayfasını açar, hangi bilgilerin gerektiğini ve otomatik girişi engelleyebilecek şeyleri (captcha, 2 adımlı doğrulama, SSO) gösterir. Hiçbir veri gönderilmez.')), anaOut,
        h('p', { class: 'muted small' }, 'Giriş, kayıt başlamadan önce APP_MON tarafından yapılır ve kayda adım olarak eklenmez; kayıt oturum açılmış sayfadan başlar. Senaryo kaydedilirken aynı bilgiler otomatik giriş olarak saklanır (parola veritabanında düz metindir; mümkünse yetkisi sınırlı bir test kullanıcısı kullanın).'),
        h('div', { class: 'inline' }, field('Kullanıcı adı', aUser), field('Parola', aPass), field('Girişliyken görünen metin (isteğe bağlı)', aText)),
        h('details', { class: 'adv' }, h('summary', null, 'Alan seçicileri (otomatik bulunamazsa)'),
          h('div', { class: 'inline' }, field('Kullanıcı adı alanı (CSS)', aSel[0]), field('Parola alanı (CSS)', aSel[1]), field('Giriş düğmesi (CSS)', aSel[2]))));
      aOn.addEventListener('change', () => { aBody.hidden = !aOn.checked; aUser.required = aPass.required = aOn.checked; });
      const f = h('form', { class: 'stack' }, h('div', { class: 'inline' }, field('Başlangıç adresi', url), start),
        h('label', { class: 'check' }, aOn, 'Bu sistemde kimlik doğrulama (giriş) var'), aBody);
      f.addEventListener('submit', async (e) => {
        e.preventDefault();
        msg.className = 'msg'; msg.textContent = aOn.checked ? 'Tarayıcı açılıyor ve giriş yapılıyor…' : 'Tarayıcı açılıyor…'; start.disabled = true;
        try {
          const body = { url: url.value.trim() };
          if (aOn.checked) {
            body.auth = { username: aUser.value, password: aPass.value, logged_in_text: aText.value, user_selector: aSel[0].value.trim(), pass_selector: aSel[1].value.trim(), submit_selector: aSel[2].value.trim() };
            preAuth = { ...body.auth, check_url: body.url };
          }
          const r = await api('/api/recorder', { method: 'POST', body });
          msg.textContent = '';
          recording(r.id, r.width, r.height);
        } catch (err) { preAuth = null; msg.className = 'msg error'; msg.textContent = err.message; start.disabled = false; }
      });
      root.replaceChildren(f, msg, out);
    }

    function recording(id, W, H) {
      let alive = true, seq = 0, queue = Promise.resolve(), textBuf = '', textTimer = null, lastURL = '';
      const img = h('img', { alt: 'Kayıt tarayıcısı', draggable: 'false' });
      const view = h('div', { class: 'rec-view', tabindex: '0', 'aria-label': 'Kayıt tarayıcısı: tıklayın ve yazın' }, img);
      const bar = h('input', { class: 'rec-bar', 'aria-label': 'Adres' });
      const go = h('form', { class: 'row rec-nav' }, bar, h('button', { type: 'submit' }, 'Git'));
      const stepsList = h('ol', { class: 'rec-steps' });
      const warn = h('p', { class: 'msg error small', role: 'alert', hidden: true });
      const hint = h('p', { class: 'muted small' }, 'Soldaki tarayıcıda normal şekilde gezin: tıklamalarınız, yazdıklarınız ve tuşlarınız adım olarak kaydedilir. Parola alanına yazılanlar ayrı ve gizli saklanır.');
      const finish = h('button', { type: 'button', class: 'primary' }, 'Kaydı bitir');
      const cancel = h('button', { type: 'button', class: 'danger' }, 'İptal');

      const post = (body) => api('/api/recorder/' + id + '/input', { method: 'POST', body }).then(applyState).catch((e) => { msg.className = 'msg error'; msg.textContent = e.message; });
      const send = (body) => { queue = queue.then(() => post(body)); return queue; };
      const flushText = () => { clearTimeout(textTimer); textTimer = null; if (textBuf) { const t = textBuf; textBuf = ''; send({ type: 'text', text: t }); } };
      const applyState = (st) => {
        if (!st) return;
        if (document.activeElement !== bar && st.url && st.url !== lastURL) { bar.value = st.url; lastURL = st.url; }
        stepsList.replaceChildren(...(st.steps || []).map((n) => h('li', null, n)));
        warn.textContent = st.blocked || '';
        warn.hidden = !st.blocked;
        stepsList.lastChild && stepsList.lastChild.scrollIntoView({ block: 'nearest' });
      };
      const coords = (ev) => { const r = img.getBoundingClientRect(); return { x: Math.round((ev.clientX - r.left) * W / r.width), y: Math.round((ev.clientY - r.top) * H / r.height) }; };

      view.addEventListener('click', (ev) => { view.focus(); flushText(); msg.textContent = ''; send({ type: 'click', ...coords(ev) }); });
      let wheelDY = 0, wheelTimer = null, wheelAt = { x: 0, y: 0 };
      view.addEventListener('wheel', (ev) => {
        ev.preventDefault();
        wheelDY += ev.deltaY; wheelAt = coords(ev);
        if (!wheelTimer) wheelTimer = setTimeout(() => { const dy = wheelDY; wheelDY = 0; wheelTimer = null; send({ type: 'scroll', ...wheelAt, dx: 0, dy }); }, 80);
      }, { passive: false });
      view.addEventListener('keydown', (ev) => {
        if ((ev.ctrlKey || ev.metaKey) && !(ev.ctrlKey && ev.altKey)) return; // Ctrl+V paste olayına bırakılır
        if (SPECIAL.has(ev.key)) { ev.preventDefault(); flushText(); send({ type: 'key', key: ev.key }); return; }
        if (ev.key.length === 1) { ev.preventDefault(); textBuf += ev.key; if (!textTimer) textTimer = setTimeout(flushText, 40); }
      });
      const onPaste = (ev) => { if (document.activeElement !== view) return; const t = ev.clipboardData && ev.clipboardData.getData('text'); if (t) { ev.preventDefault(); flushText(); send({ type: 'text', text: t }); } };
      document.addEventListener('paste', onPaste);
      go.addEventListener('submit', (ev) => { ev.preventDefault(); flushText(); send({ type: 'goto', url: bar.value.trim() }); });

      let lastURLObj = '';
      (async () => {
        while (alive) {
          try {
            const res = await fetch('/api/recorder/' + id + '/frame?after=' + seq, { headers: { 'X-API-Key': key } });
            if (res.status === 200) {
              const blob = await res.blob();
              const u = URL.createObjectURL(blob);
              img.src = u; if (lastURLObj) URL.revokeObjectURL(lastURLObj); lastURLObj = u;
              seq = Number(res.headers.get('X-Frame-Seq')) || seq;
            } else if (res.status === 204) { seq = Number(res.headers.get('X-Frame-Seq')) || seq; }
            else if (res.status === 401 || res.status === 404 || res.status === 410) { if (alive) { msg.className = 'msg error'; msg.textContent = 'Kayıt oturumu sona erdi (boşta kalma veya süre sınırı).'; alive = false; idle(); } return; }
            else await sleep(1000);
          } catch (e) { await sleep(1000); }
        }
      })();
      (async () => { while (alive) { await sleep(1500); if (!alive) break; try { applyState(await api('/api/recorder/' + id)); } catch (e) { /* oturum bitmiş olabilir */ } } })();

      const stop = async () => { alive = false; document.removeEventListener('paste', onPaste); flushText(); await queue; recCleanup = null; if (lastURLObj) URL.revokeObjectURL(lastURLObj); };
      recCleanup = () => { alive = false; document.removeEventListener('paste', onPaste); api('/api/recorder/' + id + '/stop', { method: 'POST' }).catch(() => {}); recCleanup = null; };
      cancel.addEventListener('click', async () => { await stop(); try { await api('/api/recorder/' + id + '/stop', { method: 'POST' }); } catch (e) { /* yoksay */ } msg.textContent = ''; idle(); });
      finish.addEventListener('click', async () => {
        finish.disabled = true; await stop();
        try { const d = await api('/api/recorder/' + id + '/stop', { method: 'POST' }); review(d); }
        catch (err) { msg.className = 'msg error'; msg.textContent = err.message; idle(); }
      });
      root.replaceChildren(h('div', { class: 'rec-grid' },
        h('div', { class: 'stack' }, go, view),
        h('div', { class: 'stack' }, h('h3', null, 'Kaydedilen adımlar'), stepsList, warn, hint, h('div', { class: 'row' }, finish, cancel))), msg, out);
      view.focus();
    }

    function review(d) {
      const steps = d.steps.map((st) => ({ ...st }));
      if (!steps.length) { msg.className = 'msg error'; msg.textContent = 'Hiç adım kaydedilmedi.'; idle(); return; }
      const rowsBox = h('div', { class: 'steps' });
      // Önerilen giriş aralığı: başlangıç sayfasından sonra, parola yazılan adımı ve onu izleyen gönder adımını kapsar.
      const suggested = new Set((() => {
        const sp = steps.findIndex((x) => x.secret);
        if (sp < 0) return [];
        let g = sp; while (g >= 0 && steps[g].action !== 'goto') g--;
        let end = steps.findIndex((x, i) => i > sp && (x.action === 'click' || x.action === 'press'));
        if (end < 0) end = sp;
        return steps.slice(g + 1, end + 1);
      })());
      const rowFor = (st) => {
        const row = h('fieldset', { class: 'step' },
          h('legend', null, st.action.toUpperCase()),
          h('p', { class: 'muted small mono' }, summarize(st)),
          h('div', { class: 'inline' },
            field('Adım adı', h('input', { class: 'r-name', value: st.name, maxlength: '100' })),
            field('Sayfada bulunması gereken metin', h('input', { class: 'r-text', value: st.expect_text || '', maxlength: '200', placeholder: 'isteğe bağlı' })),
            field('En fazla süre (ms)', h('input', { class: 'r-max', type: 'number', min: '0', value: st.max_ms || '', placeholder: 'sınırsız' }))),
          h('div', { class: 'row' },
            h('button', { type: 'button', class: 'danger', onclick: () => { steps.splice(steps.indexOf(st), 1); row.remove(); } }, 'Adımı sil'),
            h('label', { class: 'check r-loginwrap', hidden: true }, h('input', { class: 'r-login', type: 'checkbox' }), 'Giriş bloğunun parçası (oturum açıksa atlanır)')));
        row._st = st;
        return row;
      };
      steps.forEach((st) => rowsBox.append(rowFor(st)));
      const authPanel = h('div', { class: 'panel stack', id: 'au' },
        h('label', { class: 'check' }, h('input', { id: 'au-on', type: 'checkbox' }), 'Bu uygulamada kimlik doğrulama (giriş) var'),
        h('div', { id: 'au-body', class: 'stack', hidden: true },
          h('p', { class: 'muted small' }, 'Girişi ve oturumu APP_MON yönetir: saklanan oturum geçerliyse giriş atlanır, düşmüşse otomatik yeniden giriş yapılır. Girişin süresi ve sonucu ayrı bir adım olarak raporlanır.'),
          h('div', { class: 'seg' },
            h('label', { class: 'check' }, h('input', { type: 'radio', name: 'au-mode', value: 'form', checked: true }), 'Kullanıcı adı ve parolayı APP_MON doldursun'),
            h('label', { class: 'check' }, h('input', { type: 'radio', name: 'au-mode', value: 'steps' }), 'Kayıttaki adımlar giriş olarak çalışsın')),
          h('div', { class: 'inline' },
            field('Giriş gerektiren sayfa adresi', h('input', { id: 'au-check', placeholder: 'boş = ilk açılan adres' })),
            field('Girişliyken sayfada görünen metin (isteğe bağlı)', h('input', { id: 'au-text', maxlength: '200', placeholder: 'ör. Çıkış yap' }))),
          h('div', { id: 'au-form', class: 'stack' },
            h('div', { class: 'inline' },
              field('Kullanıcı adı', h('input', { id: 'au-user', autocomplete: 'off', maxlength: '300' })),
              field('Parola', h('input', { id: 'au-pass', type: 'password', autocomplete: 'new-password', maxlength: '300' }))),
            h('details', { class: 'adv' }, h('summary', null, 'Alan seçicileri (otomatik bulunamazsa)'),
              h('div', { class: 'inline' },
                field('Kullanıcı adı alanı (CSS)', h('input', { id: 'au-usel', placeholder: 'ör. #email' })),
                field('Parola alanı (CSS)', h('input', { id: 'au-psel', placeholder: 'ör. input[name=pw]' })),
                field('Giriş düğmesi (CSS)', h('input', { id: 'au-ssel', placeholder: 'ör. button[type=submit]' })))),
            suggested.size ? h('label', { class: 'check' }, h('input', { id: 'au-drop', type: 'checkbox', checked: true }), `Kayıttaki giriş adımlarını (${suggested.size}) çıkar; yerine otomatik giriş kullanılsın`) : null),
          h('div', { id: 'au-steps', class: 'stack', hidden: true },
            h('p', { class: 'muted small' }, 'Aşağıdaki adımlardan girişe ait olanları "Giriş bloğunun parçası" olarak işaretleyin. Parola alanına yazdığınız değer ayrı ve gizli saklanır.'),
            h('div', null, h('button', { type: 'button', id: 'au-suggest' }, 'Giriş adımlarını otomatik işaretle')))));
      const syncAuth = () => {
        const on = $('#au-on', authPanel).checked, mode = authPanel.querySelector('input[name=au-mode]:checked').value;
        $('#au-body', authPanel).hidden = !on;
        $('#au-form', authPanel).hidden = mode !== 'form';
        $('#au-steps', authPanel).hidden = mode !== 'steps';
        for (const w of rowsBox.querySelectorAll('.r-loginwrap')) w.hidden = !(on && mode === 'steps');
      };
      if (preAuth) {
        $('#au-on', authPanel).checked = true;
        $('#au-check', authPanel).value = preAuth.check_url || ''; $('#au-text', authPanel).value = preAuth.logged_in_text || '';
        $('#au-user', authPanel).value = preAuth.username; $('#au-pass', authPanel).value = preAuth.password;
        $('#au-usel', authPanel).value = preAuth.user_selector || ''; $('#au-psel', authPanel).value = preAuth.pass_selector || ''; $('#au-ssel', authPanel).value = preAuth.submit_selector || '';
        syncAuth();
      }
      const markSuggested = () => { for (const row of rowsBox.querySelectorAll('.step')) $('.r-login', row).checked = suggested.has(row._st); };
      const rvAsset = { value: '' };
      const pp2 = probePicker(probesAll, (p) => String(p.asset).toLowerCase() === rvAsset.value.trim().toLowerCase());
      const f = h('form', { class: 'stack' },
        h('div', { class: 'inline' },
          field('Asset adı', h('input', { id: 'rv-asset', required: true, maxlength: '200', list: 'rv-assets', placeholder: 'ör. mobil-uygulama' })),
          field('Senaryo adı', h('input', { id: 'rv-name', required: true, maxlength: '200', placeholder: 'ör. Giriş ve sipariş' })),
          field('Asset türü', select('rv-kind', KIND_OPTS, 'website')),
          field('Aralık (sn, en az 60)', h('input', { id: 'rv-interval', type: 'number', min: '60', value: '300' })),
          field('Adım zaman aşımı (ms)', h('input', { id: 'rv-timeout', type: 'number', min: '500', max: '60000', value: '10000' }))),
        h('datalist', { id: 'rv-assets' }, assets.map((a) => h('option', { value: a.name }))),
        pp2.el,
        d.secret_names.length ? h('p', { class: 'muted small' }, `${d.secret_names.length} gizli alan (parola) kaydedildi; değerleri sunucuda ${d.secret_names.join(', ')} değişkenlerinde saklanır (veritabanında düz metin) ve burada gösterilmez.`) : null,
        authPanel,
        rowsBox,
        h('div', { class: 'row' },
          h('span', { class: 'muted small' }, 'Her çalıştırma temiz bir tarayıcı profilinde başlar; giriş adımları da kaydedilmiş olmalıdır.'),
          h('span', { class: 'row' }, h('button', { type: 'button', id: 'rv-test' }, 'Dene (kaydetmeden)'), h('button', { type: 'submit', class: 'primary' }, 'Senaryoyu kaydet'))));
      $('#rv-asset', f).addEventListener('input', () => { rvAsset.value = $('#rv-asset', f).value; pp2.refresh(); });
      const collect = () => {
        const authOn = $('#au-on', f).checked, mode = f.querySelector('input[name=au-mode]:checked').value;
        const drop = authOn && mode === 'form' && $('#au-drop', f) && $('#au-drop', f).checked;
        const list = [...rowsBox.querySelectorAll('.step')].filter((row) => !(drop && suggested.has(row._st))).map((row) => ({
          ...row._st, name: $('.r-name', row).value.trim() || row._st.name,
          expect_text: $('.r-text', row).value || undefined, max_ms: Number($('.r-max', row).value) > 0 ? Number($('.r-max', row).value) : undefined,
          login: authOn && mode === 'steps' && $('.r-login', row).checked ? true : undefined }));
        const body = { kind: 'browser', probe_id: pp2.value() || undefined, draft_id: d.draft_id, browser_steps: list, asset_name: $('#rv-asset', f).value, name: $('#rv-name', f).value,
          asset_kind: $('#rv-kind', f).value, interval_sec: Number($('#rv-interval', f).value), timeout_ms: Number($('#rv-timeout', f).value) };
        if (authOn) {
          body.auth = { enabled: true, mode, check_url: $('#au-check', f).value.trim(), logged_in_text: $('#au-text', f).value };
          if (mode === 'form') Object.assign(body.auth, { username: $('#au-user', f).value, password: $('#au-pass', f).value,
            user_selector: $('#au-usel', f).value.trim(), pass_selector: $('#au-psel', f).value.trim(), submit_selector: $('#au-ssel', f).value.trim() });
        }
        return body;
      };
      authPanel.addEventListener('change', (ev) => {
        if (ev.target.name === 'au-mode' && ev.target.value === 'steps' && !rowsBox.querySelector('.r-login:checked')) markSuggested();
        syncAuth();
      });
      $('#au-suggest', authPanel).addEventListener('click', markSuggested);
      $('#rv-test', f).addEventListener('click', async (e) => {
        e.target.disabled = true; msg.className = 'msg'; msg.textContent = 'Tarayıcıda oynatılıyor (en fazla ~80 sn)…'; out.replaceChildren();
        try { const res = await api('/api/scenarios/test', { method: 'POST', body: collect() }); msg.textContent = ''; out.replaceChildren(stepResults(res)); }
        catch (err) { msg.className = 'msg error'; msg.textContent = err.message; }
        e.target.disabled = false;
      });
      f.addEventListener('submit', async (e) => {
        e.preventDefault(); msg.className = 'msg'; msg.textContent = '';
        try { await api('/api/scenarios', { method: 'POST', body: collect() }); render(); }
        catch (err) { msg.className = 'msg error'; msg.textContent = err.message; }
      });
      root.replaceChildren(h('h3', null, 'Kaydı gözden geçir (' + steps.length + ' adım)'), f, msg, out);
    }

    function summarize(st) {
      switch (st.action) {
        case 'goto': return 'Aç ' + st.url;
        case 'click': return 'Tıkla ' + (st.text ? '“' + st.text + '” ' : '') + (st.selector || '');
        case 'type': return (st.secret ? 'Yaz (gizli) → ' : 'Yaz “' + st.value + '” → ') + st.selector;
        default: return 'Tuş ' + st.key;
      }
    }

    idle();
    return root;
  }

  // Senaryoya isteğe bağlı probe eşleşmesi seçici. match(p): probe bu asset'e uygun mu.
  function probePicker(probes, match, cur) {
    const sel = h('select', { 'aria-label': 'Hızlı durum probe\'u' });
    const label = (p) => `${String(p.type).toUpperCase()} · ${p.target} · ${p.interval_sec} sn`;
    const rebuild = () => {
      const keep = sel.value || (cur ? String(cur) : '');
      const all = probes || [];
      const same = all.filter(match), other = all.filter((p) => !match(p));
      const grp = (t, l) => (l.length ? h('optgroup', { label: t }, l.map((p) => h('option', { value: String(p.id) }, label(p) + (t.startsWith('Başka') ? ' · ' + p.asset : '')))) : null);
      sel.replaceChildren(h('option', { value: '' }, '— yok (yalnızca senaryo sonucu) —'),
        ...[grp('Aynı asset', same), grp('Başka asset\'ler', other)].filter(Boolean));
      sel.value = all.some((p) => String(p.id) === keep) ? keep : '';
    };
    rebuild();
    return { el: h('div', null, field('Hızlı durum probe\'u (isteğe bağlı)', sel),
      h('p', { class: 'muted small' }, 'Seçerseniz genel bakıştaki kart, bu probe üst üste 2 kez başarısız olunca senaryoyu beklemeden kırmızıya döner. Probe farklı bir asset\'te tanımlıysa "Başka asset\'ler" altından seçin. Seçmezseniz kart yalnızca senaryo sonucuna göre boyanır.')),
      refresh: rebuild, value: () => (sel.value ? Number(sel.value) : 0) };
  }

  // ---- Keşfet: web sitesi keşfi (spider, yol sözlüğü, oturumlu sayfalar) ----
  const CRAWL_ST = { pending: ['warn', 'Onay bekliyor'], approved: ['info', 'Sırada'], running: ['info', 'Çalışıyor'], done: ['up', 'Bitti'], failed: ['down', 'Başarısız'], rejected: ['down', 'Reddedildi'], cancelled: ['unknown', 'İptal'] };
  async function crawlSection(scenarios) {
    const [jobs, meta] = await Promise.all([api('/api/crawl').catch(() => []), api('/api/crawl/wordlists').catch(() => ({ lists: [], spider: false }))]);
    const admin = ME.role === 'admin';
    const msg = h('p', { class: 'msg', role: 'status' });
    const resBox = h('div');
    const authSc = scenarios.filter((x) => x.kind === 'browser' && x.auth && !x.auth.blocked);
    // seçenekler: hiçbiri varsayılan seçili değildir
    const url = h('input', { type: 'url', required: true, maxlength: '2000', placeholder: 'https://www.sirketiniz.com' });
    const cSpider = h('input', { type: 'checkbox' }), cWords = h('input', { type: 'checkbox' }), cAuth = h('input', { type: 'checkbox' });
    cSpider.disabled = !meta.spider;
    const wl = h('select', { 'aria-label': 'Sözlük' }, meta.lists.map((l) => h('option', { value: l.id }, `${l.name.split(' — ')[0]} (${l.count} yol)`)));
    const wlInfo = h('p', { class: 'muted small' });
    const syncWl = () => { const l = meta.lists.find((x) => x.id === wl.value); wlInfo.textContent = l ? l.name + '. Yaygın bir yol sözlüğü yalnızca herkese açık, izlemeye değer uç noktaları (sağlık, durum, API belgesi, giriş sayfası…) dener; yedek/yapılandırma dosyası gibi hassas yollar listede yoktur.' : ''; };
    wl.addEventListener('change', syncWl); syncWl();
    const extra = h('textarea', { rows: '3', maxlength: '4000', placeholder: 'Ek yollar (her satıra bir tane), ör.\n/api/siparisler\n/durum', 'aria-label': 'Ek yollar' });
    const scSel = h('select', { 'aria-label': 'Giriş yapılacak senaryo' }, authSc.map((x) => h('option', { value: String(x.id) }, `${x.asset} / ${x.name}`)));
    const pages = h('input', { type: 'number', min: '1', max: '100', value: '30' }), depth = h('input', { type: 'number', min: '0', max: '3', value: '2' }), delay = h('input', { type: 'number', min: '200', max: '10000', value: '500' });
    const sub = (box, ...kids) => { const d = h('div', { class: 'sub stack', hidden: true }, ...kids); box.addEventListener('change', () => { d.hidden = !box.checked; }); return d; };
    const opt = (box, title, desc, extraNode) => h('div', { class: 'opt' }, h('label', { class: 'check' }, box, h('b', null, title)), h('p', { class: 'muted small' }, desc), extraNode);
    const submit = h('button', { type: 'submit', class: 'primary' }, admin ? 'Keşfi başlat…' : 'Onaya gönder…');
    const f = h('form', { class: 'stack' },
      field('Başlangıç adresi', url),
      opt(cSpider, 'Siteyi gez (spider)', meta.spider ? 'Gerçek bir tarayıcıyla aynı alan adındaki sayfaları gezer; formları, yüklenen kaynakları, arka plan API çağrılarını ve dış alan adlarını çıkarır. Hiçbir forma veri girilmez, hiçbir düğmeye tıklanmaz.' : 'Sunucuda tarayıcı özelliği kapalı (APM_BROWSER_ENABLED).', null),
      opt(cWords, 'Yaygın yol sözlüğü', 'Bağlantısı verilmeyen ama sık kullanılan yolları tek tek dener.', sub(cWords, field('Sözlük', wl), wlInfo, field('Ek yollar', extra))),
      opt(cAuth, 'Oturumlu sayfalar (giriş gerektiren)', authSc.length ? 'Kayıtlı bir tarayıcı senaryosunun giriş bilgisiyle oturum açılır ve giriş sonrası sayfalar da gezilir. Yalnızca senaryonun giriş yaptığı alan adında çalışır; çıkış/silme bağlantıları gezilmez. Kullandığınız hesap salt-okunur, ayrılmış bir test kullanıcısı olmalıdır.' : 'Önce Senaryolar bölümünde, "Giriş gerektirir" ayarlı bir tarayıcı senaryosu kaydedin.', authSc.length ? sub(cAuth, field('Senaryo', scSel)) : null),
      h('details', { class: 'adv' }, h('summary', null, 'Sınırlar'), h('div', { class: 'inline' }, field('En çok sayfa (≤100)', pages), field('En çok derinlik (≤3)', depth), field('İstekler arası bekleme (ms)', delay)),
        h('p', { class: 'muted small' }, 'robots.txt her zaman uygulanır (Disallow ve Crawl-delay). Bekleme en az 200 ms\'dir.')),
      h('div', { class: 'row' }, submit), msg);
    cAuth.disabled = !authSc.length;
    const send = async (body, close) => {
      try { const r = await api('/api/crawl', { method: 'POST', body }); close(); render(); return r; }
      catch (e) { msg.className = 'msg error'; msg.textContent = e.message; close(); }
    };
    f.addEventListener('submit', (e) => {
      e.preventDefault(); msg.className = 'msg'; msg.textContent = '';
      const body = { url: url.value.trim(), spider: cSpider.checked, wordlist: cWords.checked ? wl.value : '', extra: cWords.checked ? extra.value.split('\n').map((x) => x.trim()).filter(Boolean) : [],
        max_pages: Number(pages.value), max_depth: Number(depth.value), delay_ms: Number(delay.value), scenario_id: cAuth.checked ? Number(scSel.value) : 0 };
      if (!body.spider && !body.wordlist) { msg.className = 'msg error'; msg.textContent = 'En az bir seçenek işaretleyin: siteyi gez veya yol sözlüğü.'; return; }
      if (cAuth.checked && !body.spider) { msg.className = 'msg error'; msg.textContent = 'Oturumlu tarama için "Siteyi gez" de işaretli olmalı.'; return; }
      const decl = h('input', { type: 'checkbox' });
      const ok = h('button', { type: 'button', class: 'primary', disabled: true }, admin ? 'Onaylıyorum, başlat' : 'Onaylıyorum, yönetici onayına gönder');
      decl.addEventListener('change', () => { ok.disabled = !decl.checked; });
      let close = () => {};
      ok.addEventListener('click', () => { ok.disabled = true; send({ ...body, declared: true }, close); });
      const what = [body.spider ? 'siteyi gezecek (sayfalar, kaynaklar, API çağrıları)' : null, body.wordlist ? `yaygın yol sözlüğünü deneyecek (${(meta.lists.find((l) => l.id === body.wordlist) || {}).count || 0} yol${body.extra.length ? ' + ' + body.extra.length + ' ek yol' : ''})` : null, body.scenario_id ? 'kayıtlı giriş bilgisiyle oturum açıp giriş sonrası sayfalara da bakacak' : null].filter(Boolean);
      close = openModal('Keşif başlatılsın mı?', h('div', { class: 'stack' },
        h('div', { class: 'callout warn' }, h('b', null, 'Dikkat: '), 'Bu işlem hedef siteye çok sayıda istek gönderir. Yalnızca sahibi olduğunuz veya izleme/test yetkisi aldığınız sitelerde kullanın; izinsiz tarama hukuka ve site kurallarına aykırı olabilir.'),
        h('p', null, h('b', { class: 'mono' }, body.url), ' adresinde APP_MON ', what.join('; ') + '.'),
        h('ul', { class: 'small' }, h('li', null, 'robots.txt kurallarına uyulur; yasak yollar gezilmez.'), h('li', null, `En çok ${body.max_pages} sayfa, derinlik ${body.max_depth}, istekler arası ${body.delay_ms} ms.`),
          h('li', null, 'Form gönderilmez, düğmeye tıklanmaz; çıkış/silme bağlantıları atlanır. Güvenlik açığı taraması yapılmaz.'),
          body.scenario_id ? h('li', null, 'Oturumlu taramada giriş sayfası başarısız olursa hesap kilitlenmesin diye tekrar denenmez.') : null,
          h('li', null, 'İstek ve sonuç denetim günlüğüne kaydedilir. İstekler "' + 'APP_MON-Discovery/1' + '" kimliğiyle gider; site sahibi günlüklerde görebilir.')),
        admin ? h('p', { class: 'muted small' }, 'Yönetici olduğunuz için onayınız bu işi doğrudan başlatır.') : h('p', { class: 'muted small' }, 'İşletmeci olarak isteğiniz bir yöneticinin onayına gider; onaylanınca çalışır.'),
        h('label', { class: 'check' }, decl, 'Bu sitenin sahibi olduğumu veya izleme yetkisi bulunduğunu beyan ederim.'),
        h('div', { class: 'row' }, ok, h('button', { type: 'button', onclick: () => close() }, 'Vazgeç'))), false);
    });

    // ---- sonuç görünümü ----
    const showResult = async (job) => {
      resBox.replaceChildren(h('p', { class: 'muted' }, 'yükleniyor…'));
      let d; try { d = await api('/api/crawl/' + job.id); } catch (e) { resBox.replaceChildren(h('p', { class: 'msg error' }, e.message)); return; }
      const r = d.result; if (!r) { resBox.replaceChildren(h('p', { class: 'muted' }, d.error || 'Henüz sonuç yok.')); return; }
      const picked = new Map(); const pmsg = h('p', { class: 'msg', role: 'status' });
      const assetIn = h('input', { maxlength: '200', value: (() => { try { return new URL(r.start).host; } catch (e) { return ''; } })(), 'aria-label': 'Asset adı' });
      const addBtn = h('button', { type: 'button', class: 'primary' }, 'Seçilenleri izlemeye ekle');
      const upd = () => { addBtn.disabled = !picked.size; addBtn.textContent = picked.size ? `Seçilen ${picked.size} adresi izlemeye ekle` : 'Seçilenleri izlemeye ekle'; };
      upd();
      const pick = (id, probe) => { const c = h('input', { type: 'checkbox', 'aria-label': 'Seç' }); c.addEventListener('change', () => { if (c.checked) picked.set(id, probe); else picked.delete(id); upd(); }); return h('td', null, c); };
      const httpProbe = (u) => ({ type: 'http', target: u, interval_sec: 60, timeout_ms: 10000, http_method: 'GET', expect_status: '', expect_text: '', headers: {}, body: '', verify_tls: true, follow_redirects: true });
      const code = (n) => (n >= 200 && n < 400 ? pill('up', String(n)) : n ? pill('down', String(n)) : pill('unknown', '—'));
      const sz = (b) => (b >= 1048576 ? (b / 1048576).toFixed(1) + ' MB' : b >= 1024 ? Math.round(b / 1024) + ' KB' : (b || 0) + ' B');
      const short = (u) => { try { const x = new URL(u); return x.pathname + x.search; } catch (e) { return u; } };
      const tabs = [
        ['Sayfalar', r.pages.length, () => table(['', 'Sayfa', 'Başlık', 'Durum', 'Süre', 'Bağlantı', 'Form'], r.pages.map((p) => h('tr', null, pick('p' + p.url, httpProbe(p.url)),
          h('td', { class: 'mono small' }, short(p.url), p.auth ? [' ', pill('info', 'oturumlu')] : null), h('td', { class: 'small' }, p.title || (p.error ? p.error : '—')), h('td', null, code(p.status)),
          h('td', { class: 'mono' }, p.ms ? Math.round(p.ms) + ' ms' : '—'), h('td', { class: 'mono' }, String(p.links)),
          h('td', { class: 'small' }, (p.forms || []).map((fm) => `${fm.method.toUpperCase()} ${short(fm.action)}${fm.password ? ' 🔒' : ''}`).join(', ') || '—'))), 'Sayfa bulunamadı.', PAGE)],
        ['API çağrıları', r.endpoints.length, () => table(['', 'Yöntem', 'Adres', 'Durum', 'En yavaş', 'Sayı'], r.endpoints.map((e) => h('tr', null, e.method === 'GET' ? pick('e' + e.url, httpProbe(e.url)) : h('td'),
          h('td', { class: 'mono' }, e.method), h('td', { class: 'mono small' }, e.third ? [pill('warn', 'dış'), ' ', e.url] : short(e.url)), h('td', null, code(e.status)), h('td', { class: 'mono' }, Math.round(e.ms) + ' ms'), h('td', { class: 'mono' }, String(e.count)))), 'Arka plan çağrısı görülmedi.', PAGE)],
        ['Kaynaklar', r.resources.length, () => table(['', 'Tür', 'Adres', 'Durum', 'Süre', 'Boyut', 'Sayfa'], r.resources.map((x) => h('tr', null, h('td'), h('td', { class: 'small' }, x.type), h('td', { class: 'mono small' }, x.third ? [pill('warn', 'dış'), ' ', x.host, short(x.url)] : short(x.url)),
          h('td', null, x.failed ? pill('down', 'hata') : code(x.status)), h('td', { class: 'mono' }, Math.round(x.ms) + ' ms'), h('td', { class: 'mono' }, sz(x.bytes)), h('td', { class: 'mono' }, String(x.pages)))), 'Kaynak yok.', PAGE)],
        ['Alan adları', r.hosts.length, () => table(['', 'Alan adı', 'Tür', 'İstek', 'Hata', 'Sertifika'], r.hosts.map((x) => h('tr', null, x.tls_days != null ? pick('h' + x.host, { type: 'tls', target: x.host.includes(':') ? x.host : x.host + ':443', interval_sec: 3600, timeout_ms: 10000, verify_tls: true }) : h('td'),
          h('td', { class: 'mono' }, x.host), h('td', null, x.third ? pill('warn', 'dış') : pill('up', 'kendi')), h('td', { class: 'mono' }, String(x.requests)), h('td', { class: 'mono' }, `${x.failed + x.errors}`),
          h('td', { class: 'small' }, x.tls_days != null ? x.tls_days + ' gün' : '—'))), 'Alan adı yok.', PAGE)],
        ['Sözlük bulguları', (r.hits || []).length, () => table(['', 'Yol', 'Durum', 'Boyut', 'Yönlendirme'], (r.hits || []).map((x) => h('tr', null, [200, 204].includes(x.status) ? pick('w' + x.path, httpProbe(new URL(x.path, r.start).href)) : h('td'),
          h('td', { class: 'mono' }, x.path), h('td', null, code(x.status)), h('td', { class: 'mono' }, sz(x.bytes)), h('td', { class: 'mono small' }, x.redirect || '—'))), 'Sözlükten bulunan yol yok.', PAGE)],
        ['Atlananlar', (r.robots_skipped || []).length, () => table(['robots.txt nedeniyle gezilmeyen yol'], (r.robots_skipped || []).map((x) => h('tr', null, h('td', { class: 'mono' }, x))), 'robots.txt hiçbir yolu engellemedi.', PAGE)],
      ];
      const body = h('div'); let cur = 0;
      const nav = h('div', { class: 'tabs', role: 'tablist' });
      const draw = () => { nav.replaceChildren(...tabs.map(([n, c], i) => h('button', { type: 'button', role: 'tab', class: 'tab' + (i === cur ? ' on' : ''), 'aria-selected': String(i === cur), onclick: () => { cur = i; draw(); } }, `${n} (${c})`))); body.replaceChildren(tabs[cur][2]()); };
      draw();
      addBtn.addEventListener('click', async () => {
        addBtn.disabled = true; let ok = 0, bad = 0, last = '';
        for (const p of picked.values()) { try { await api('/api/probes', { method: 'POST', body: { asset_name: assetIn.value.trim(), ...p } }); ok++; } catch (e) { bad++; last = e.message; } }
        pmsg.className = 'msg ' + (bad ? 'error' : 'ok'); pmsg.textContent = `${ok} probe eklendi.` + (bad ? ` ${bad} eklenemedi: ${last}` : ''); upd();
      });
      resBox.replaceChildren(h('div', { class: 'panel stack' }, h('h3', null, 'Keşif sonucu: ' + r.start),
        h('p', { class: 'muted small' }, `robots.txt: ${r.robots} · ${r.pages.length} sayfa, ${r.endpoints.length} API çağrısı, ${r.resources.length} kaynak, ${r.hosts.length} alan adı${r.authed ? ' · oturumlu' : ''} · ${Math.round(r.seconds)} sn` + (d.error ? ' · ' + d.error : '')),
        (r.notes || []).map((n) => h('p', { class: 'muted small' }, 'ℹ ' + n)), nav, body,
        h('div', { class: 'inline' }, field('Eklenecek asset adı', assetIn), addBtn), pmsg));
      resBox.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
    };

    const act = (label, cls, fn) => h('button', { type: 'button', class: 'small ' + (cls || ''), onclick: async (e) => { e.target.disabled = true; try { await fn(); render(); } catch (x) { alert(x.message); e.target.disabled = false; } } }, label);
    const rows = jobs.map((j) => { const [c, t] = CRAWL_ST[j.status] || ['unknown', j.status];
      return h('tr', null, h('td', { class: 'mono small' }, '#' + j.id), h('td', { class: 'mono small' }, j.url), h('td', { class: 'small' }, j.summary),
        h('td', null, pill(c, t), j.progress ? h('div', { class: 'muted small' }, j.progress) : null, j.error ? h('div', { class: 'small' }, j.error) : null),
        h('td', { class: 'small' }, j.requested_by, h('div', { class: 'muted' }, ago(j.created_at)), j.decided_by ? h('div', { class: 'muted' }, 'onay: ' + j.decided_by) : null),
        h('td', { class: 'actions' },
          j.status === 'done' || (j.status === 'failed' && j.pages) ? h('button', { type: 'button', class: 'small', onclick: () => showResult(j) }, 'Sonuç') : null,
          j.status === 'pending' && admin ? [act('Onayla', 'primary', () => api('/api/crawl/' + j.id + '/approve', { method: 'POST' })), act('Reddet', '', () => api('/api/crawl/' + j.id + '/reject', { method: 'POST' }))] : null,
          ['running', 'approved'].includes(j.status) ? act('Durdur', 'danger', () => api('/api/crawl/' + j.id, { method: 'DELETE' })) : act('Sil', '', () => api('/api/crawl/' + j.id, { method: 'DELETE' })))); });
    const pendingN = jobs.filter((j) => j.status === 'pending').length;
    return h('section', null, h('h3', null, 'Keşfet: web sitesi bileşenlerini bul'),
      h('p', { class: 'muted' }, 'Bir adresten başlayarak sitenin sayfalarını, formlarını, yüklenen kaynaklarını, arka plan API çağrılarını ve dış bağımlılıklarını çıkarır; bulduklarınızdan seçtiklerinizi tek tıkla probe olarak izlemeye alırsınız. Tüm seçenekler kapalı gelir, başlatmadan önce sahiplik beyanı istenir ve robots.txt\'e uyulur.'),
      admin && pendingN ? h('p', { class: 'callout warn' }, `${pendingN} keşif isteği yönetici onayı bekliyor.`) : null,
      h('div', { class: 'panel' }, f), table(['#', 'Adres', 'Seçenekler', 'Durum', 'İsteyen', ''], rows, 'Henüz keşif yapılmadı.', PAGE), resBox);
  }

  async function viewSenaryolar() {
    await refreshHeader();
    const [list, assets, probesAll, cap] = await Promise.all([api('/api/scenarios'), api('/api/assets'), api('/api/probes').catch(() => []), api('/api/scenarios/capacity').catch(() => null)]);
    const capNote = !cap || !cap.browser_scenarios ? null : h('p', { class: cap.overloaded ? 'error small' : 'muted small', role: 'status' },
      h('span', null, 'Kapasite: ' + cap.browser_scenarios + ' tarayıcı senaryosu, aynı anda ' + cap.browser_concurrency + ' oynatma; ortalama süre ~' + Math.round(cap.avg_browser_ms / 1000) + ' sn, tahmini tur süresi ~' + cap.est_round_sec + ' sn.'),
      cap.overloaded ? h('span', null, ' Ortalama aralık bu süreden kısa: senaryolar aralığında çalışamaz. Aralıkları uzatın veya sunucuda APM_BROWSER_CONCURRENCY değerini artırın (her oynatma yaklaşık 300 MB bellek kullanır).') : null);
    const msg = h('p', { class: 'msg', role: 'status' });
    const out = h('div');
    const stepsBox = h('div', { class: 'steps' });
    const addStep = () => {
      const c = stepCard(stepsBox.children.length + 1, () => { c.remove(); [...stepsBox.children].forEach((x, i) => { $('legend', x).textContent = 'Adım ' + (i + 1); }); });
      stepsBox.append(c);
    };
    addStep();
    const scAsset = { value: '' };
    const pp1 = probePicker(probesAll, (p) => String(p.asset).toLowerCase() === scAsset.value.trim().toLowerCase());
    const f = h('form', { id: 'sc-form', class: 'stack' },
      h('div', { class: 'inline' },
        field('Asset adı', h('input', { id: 'sc-asset', required: true, maxlength: '200', list: 'sc-assets', placeholder: 'ör. mobil-uygulama' })),
        field('Senaryo adı', h('input', { id: 'sc-name', required: true, maxlength: '200', placeholder: 'ör. Giriş ve liste' })),
        field('Asset türü', select('sc-kind', KIND_AUTO)),
        field('Aralık (sn, en az 30)', h('input', { id: 'sc-interval', type: 'number', min: '30', value: '300' })),
        field('Toplam zaman aşımı (ms)', h('input', { id: 'sc-timeout', type: 'number', min: '500', max: '60000', value: '10000' })),
        h('label', { class: 'check' }, h('input', { id: 'sc-tls', type: 'checkbox', checked: true }), 'TLS sertifikasını doğrula')),
      h('datalist', { id: 'sc-assets' }, assets.map((a) => h('option', { value: a.name }))),
      pp1.el,
      field('Değişkenler (satır başına "ad=değer"; adımlarda {{ad}} olarak kullanılır)', area('sc-vars', 3, 'kullanici=test\nparola=...')),
      stepsBox,
      h('div', { class: 'row' },
        h('button', { type: 'button', onclick: addStep }, '+ Adım ekle'),
        h('span', { class: 'row' },
          h('button', { type: 'button', id: 'sc-test' }, 'Dene (kaydetmeden)'),
          h('button', { type: 'submit', class: 'primary' }, 'Senaryoyu kaydet'))));
    $('#sc-asset', f).addEventListener('input', () => { scAsset.value = $('#sc-asset', f).value; pp1.refresh(); });
    const collect = () => ({
      probe_id: pp1.value() || undefined, asset_name: $('#sc-asset', f).value, name: $('#sc-name', f).value, interval_sec: Number($('#sc-interval', f).value),
      timeout_ms: Number($('#sc-timeout', f).value), verify_tls: $('#sc-tls', f).checked, asset_kind: $('#sc-kind', f).value || undefined,
      vars: parseVars($('#sc-vars', f).value), steps: readSteps(stepsBox) });
    $('#sc-test', f).addEventListener('click', async () => {
      msg.className = 'msg'; msg.textContent = 'Deneniyor…'; out.replaceChildren();
      try { const res = await api('/api/scenarios/test', { method: 'POST', body: collect() }); msg.textContent = ''; out.replaceChildren(stepResults(res)); }
      catch (err) { msg.className = 'msg error'; msg.textContent = err.message; }
    });
    f.addEventListener('submit', async (e) => {
      e.preventDefault();
      msg.className = 'msg'; msg.textContent = '';
      try { await api('/api/scenarios', { method: 'POST', body: collect() }); render(); }
      catch (err) { msg.className = 'msg error'; msg.textContent = err.message; }
    });

    const detail = h('div');
    const showRuns = async (sc) => {
      const runs = await api('/api/scenarios/' + sc.id + '/runs');
      const runDetail = h('div');
      const openRun = (r) => {
        const steps = r.steps || [];
        runDetail.replaceChildren(h('div', { class: 'panel stack' }, h('h3', null, 'Çalıştırma ayrıntısı · ' + ago(r.at)),
          ...steps.map((st, i) => {
            const shot = h('div');
            const chg = (st.added || []).length + (st.removed || []).length;
            return h('div', { class: 'run-step' },
              h('div', { class: 'row' }, h('span', null, h('b', null, (i + 1) + '. ' + st.name), ' ', st.ok ? pill('up', 'Tamam') : pill('down', 'Hata')),
                h('span', { class: 'mono small' }, fmt(st.ms) + ' ms' + (st.page ? ' · ' + st.page : ''))),
              st.error ? h('p', { class: 'error small' }, st.error) : null,
              st.ignored ? h('p', { class: 'muted small' }, st.ignored + ' öğe dinamik içerik sayılıp (sayaç, slayt gösterisi vb.) karşılaştırmaya alınmadı.') : null,
              st.muted ? h('p', { class: 'muted small' }, st.muted + ' öğe yoksay kurallarıyla karşılaştırmadan çıkarıldı/maskelendi.') : null,
              chg ? h('div', { class: 'diff' }, h('p', { class: 'small' }, pill('warning', 'Sayfa yapısı değişti'), ' önceki çalıştırmaya göre'),
                ...(st.added || []).map((x) => h('div', { class: 'mono small ok' }, '+ ' + x)), ...(st.removed || []).map((x) => h('div', { class: 'mono small error' }, '− ' + x))) : null,
              st.shot ? h('div', null, h('button', { type: 'button', onclick: async (ev) => {
                ev.target.disabled = true;
                try {
                  const res = await fetch('/api/scenarios/' + sc.id + '/runs/' + r.id + '/shots/' + i, { headers: { 'X-API-Key': key } });
                  if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error || 'HTTP ' + res.status);
                  shot.replaceChildren(h('img', { class: 'shot', alt: st.name + ' ekran görüntüsü', src: URL.createObjectURL(await res.blob()) }));
                } catch (x) { shot.replaceChildren(h('p', { class: 'muted small' }, x.message)); }
                ev.target.disabled = false;
              } }, 'Ekran görüntüsü'), shot) : null);
          })));
      };
      detail.replaceChildren(h('section', null, h('h3', null, sc.asset + ' / ' + sc.name + ' — son çalıştırmalar'),
        table(['Zaman', 'Sonuç', 'Süre', 'Ayrıntı'], runs.map((r) => h('tr', { class: sc.kind === 'browser' ? 'click' : null, onclick: sc.kind === 'browser' ? () => openRun(r) : null },
          h('td', { class: 'mono small' }, ago(r.at)), h('td', null, r.ok ? pill('up', 'Başarılı') : pill('down', 'Başarısız')),
          h('td', { class: 'mono' }, fmt(r.duration_ms) + ' ms'),
          h('td', { class: 'small' }, r.error || (r.steps || []).map((x) => x.name + ' ' + fmt(x.ms) + ' ms').join(' → ') + ((r.steps || []).some((x) => (x.added || []).length + (x.removed || []).length) ? ' · sayfa değişti' : '')))), 'Henüz çalıştırma yok.', PAGE),
        sc.kind === 'browser' ? h('p', { class: 'muted small' }, 'Adım ayrıntısı, ekran görüntüsü ve değişiklikler için bir çalıştırmaya tıklayın.') : null, runDetail));
    };
    const retryForm = (sc) => {
      const u = h('input', { autocomplete: 'off', maxlength: '300', value: (sc.auth && sc.auth.username) || '' });
      const p = h('input', { type: 'password', autocomplete: 'new-password', maxlength: '300', placeholder: 'yeni parola' });
      const go = async (withNew) => {
        msg.className = 'msg'; msg.textContent = '';
        const body = withNew ? { username: u.value, password: p.value } : {};
        if (withNew && (!u.value || !p.value)) { msg.className = 'msg error'; msg.textContent = 'Kullanıcı adı ve parola birlikte girilmeli.'; return; }
        try { await api('/api/scenarios/' + sc.id + '/retry-login', { method: 'POST', body }); render(); } catch (x) { msg.className = 'msg error'; msg.textContent = x.message; }
      };
      out.replaceChildren(h('div', { class: 'panel stack' }, h('h3', null, 'Girişi yeniden dene: ' + sc.name),
        h('p', { class: 'muted small' }, 'Hesabın kilitlenmemesi için otomatik giriş durduruldu. Parolayı site tarafında doğruladıktan sonra yeni bilgileri girin; giriş yeniden etkinleşir ve bir sonraki çalıştırmada denenir.'),
        h('div', { class: 'inline' }, field('Kullanıcı adı', u), field('Yeni parola', p)),
        h('div', { class: 'row' }, h('button', { type: 'button', class: 'primary', onclick: () => go(true) }, 'Kaydet ve yeniden etkinleştir'), h('button', { type: 'button', onclick: () => go(false) }, 'Bilgileri değiştirmeden yeniden dene'))));
      out.scrollIntoView({ block: 'center', behavior: 'smooth' });
    };
    const editBox = h('div');
    const editForm = (sc) => {
      const isB = sc.kind === 'browser';
      const emsg = h('p', { class: 'msg', role: 'status' });
      const nm = h('input', { value: sc.name, maxlength: '200', required: true });
      const iv = h('input', { type: 'number', value: String(sc.interval_sec), min: isB ? '60' : '30' });
      const to = h('input', { type: 'number', value: String(sc.timeout_ms), min: '500', max: '60000' });
      const tls = h('input', { type: 'checkbox' }); tls.checked = sc.verify_tls;
      const en = h('input', { type: 'checkbox' }); en.checked = sc.enabled;
      // adımlar: from = eski sıra
      let items = sc.steps.map((st, i) => ({ from: i, ...st }));
      const stepsBox = h('div', { class: 'stack' });
      const inp = (v, ph, w) => h('input', { value: v == null ? '' : String(v), placeholder: ph, style: null });
      const drawSteps = () => {
        stepsBox.replaceChildren(...items.map((it, i) => {
          const refs = {};
          const mk = (k, label, ph, type) => { refs[k] = h('input', { value: it[k] == null ? '' : String(it[k]), placeholder: ph || '', type: type || 'text' }); return field(label, refs[k]); };
          const sync = () => { for (const k in refs) it[k] = refs[k].type === 'number' ? Number(refs[k].value || 0) : refs[k].value; if (refs.login) it.login = refs.login.checked; };
          const fields = [mk('name', 'Adım adı')];
          if (isB) {
            if (it.action === 'goto') fields.push(mk('url', 'Adres'));
            else { fields.push(mk('selector', 'Seçici (CSS)')); if (it.action === 'type' && !it.secret) fields.push(mk('value', 'Yazılan metin')); if (it.action === 'press') fields.push(mk('key', 'Tuş')); }
            fields.push(mk('expect_text', 'Beklenen metin', 'isteğe bağlı'), mk('max_ms', 'En çok süre (ms)', '0 = sınırsız', 'number'));
          } else {
            fields.push(mk('method', 'Yöntem'), mk('url', 'Adres'), mk('expect_status', 'Beklenen durum', 'ör. 200 veya 200-299'), mk('expect_text', 'Beklenen metin', 'isteğe bağlı'), mk('max_ms', 'En çok süre (ms)', '0 = sınırsız', 'number'));
          }
          const bar = h('span', { class: 'row' },
            h('button', { type: 'button', disabled: i === 0, onclick: () => { sync(); [items[i - 1], items[i]] = [items[i], items[i - 1]]; drawSteps(); } }, '↑'),
            h('button', { type: 'button', disabled: i === items.length - 1, onclick: () => { sync(); [items[i + 1], items[i]] = [items[i], items[i + 1]]; drawSteps(); } }, '↓'),
            h('button', { type: 'button', class: 'danger', disabled: items.length === 1, onclick: () => { items.splice(i, 1); drawSteps(); } }, 'Sil'));
          const box = h('fieldset', { class: 'step-edit' }, h('legend', null, `Adım ${i + 1} · ${it.method || ''}${it.secret ? ' · gizli değer' : ''}`), h('div', { class: 'inline' }, fields), bar);
          box.addEventListener('input', sync); box.addEventListener('change', sync);
          return box;
        }));
      };
      drawSteps();
      // kimlik doğrulama (tarayıcı)
      let authBox = null, getAuth = () => undefined;
      if (isB) {
        const au = sc.auth || {};
        const aOn = h('input', { type: 'checkbox' }); aOn.checked = !!sc.auth;
        const aMode = select(null, [['form', 'Kullanıcı adı/parola (otomatik doldur)'], ['steps', 'Kayıttaki adımlar giriş olarak çalışsın']], au.mode || 'form');
        const aUser = h('input', { value: au.username || '', autocomplete: 'off' });
        const aPass = h('input', { type: 'password', autocomplete: 'new-password', placeholder: sc.auth ? 'değiştirmek için yazın (boş = aynı)' : '' });
        const aCheck = h('input', { value: au.check_url || '', placeholder: 'giriş gerektiren sayfa' });
        const aText = h('input', { value: au.logged_in_text || '' });
        const aSel = [au.user_selector, au.pass_selector, au.submit_selector].map((v) => h('input', { value: v || '' }));
        authBox = h('fieldset', { class: 'step-edit' }, h('legend', null, 'Kimlik doğrulama'),
          h('label', { class: 'check' }, aOn, 'Bu senaryoda giriş var'),
          h('div', { class: 'inline' }, field('Mod', aMode), field('Kullanıcı adı', aUser), field('Parola', aPass)),
          h('div', { class: 'inline' }, field('Giriş gerektiren adres', aCheck), field('Girişliyken görünen metin', aText)),
          h('div', { class: 'inline' }, field('Kullanıcı adı alanı (CSS)', aSel[0]), field('Parola alanı (CSS)', aSel[1]), field('Giriş düğmesi (CSS)', aSel[2])),
          h('p', { class: 'muted small' }, 'Kullanıcı adı, parola veya seçiciler değişirse kayıtlı oturum silinir ve giriş hata sayacı sıfırlanır.'));
        getAuth = () => ({ enabled: aOn.checked, mode: aMode.value, username: aUser.value, password: aPass.value, check_url: aCheck.value.trim(), logged_in_text: aText.value, user_selector: aSel[0].value.trim(), pass_selector: aSel[1].value.trim(), submit_selector: aSel[2].value.trim() });
      }
      const save = h('button', { type: 'submit', class: 'primary' }, 'Kaydet');
      const pp3 = probePicker(probesAll, (p) => p.asset_id === sc.asset_id, sc.probe_id);
      const form = h('form', { class: 'stack' },
        h('div', { class: 'inline' }, field('Senaryo adı', nm), field('Aralık (sn)', iv), field('Zaman aşımı (ms)', to)),
        pp3.el,
        h('div', { class: 'row' }, h('label', { class: 'check' }, en, 'Etkin'), h('label', { class: 'check' }, tls, 'TLS sertifikasını doğrula')),
        authBox, h('h4', null, 'Adımlar'),
        h('p', { class: 'muted small' }, 'Başlık, gövde ve değişkenler gibi gizli alanlar korunur; burada yalnızca görünen alanlar düzenlenir. Adımları ↑/↓ ile sıralayabilir veya silebilirsiniz. Adımlar değişirse sayfa değişikliği taban çizgisi yeniden başlar.'),
        stepsBox, h('div', { class: 'row' }, save, h('button', { type: 'button', onclick: () => editBox.replaceChildren() }, 'Vazgeç')), emsg);
      form.addEventListener('submit', async (e) => {
        e.preventDefault(); save.disabled = true; emsg.className = 'msg'; emsg.textContent = 'Kaydediliyor…';
        const body = { name: nm.value, interval_sec: Number(iv.value), timeout_ms: Number(to.value), verify_tls: tls.checked, enabled: en.checked, probe_id: pp3.value(),
          steps: items.map((it) => ({ from: it.from, name: it.name || '', method: it.method || '', url: it.url || '', expect_status: it.expect_status || '', expect_text: it.expect_text || '', max_ms: Number(it.max_ms || 0), selector: it.selector || '', value: it.value || '', key: it.key || '', login: !!it.login })) };
        const a = getAuth(); if (a) body.auth = a;
        try { await api('/api/scenarios/' + sc.id, { method: 'PUT', body }); render(); } catch (x) { emsg.className = 'msg error'; emsg.textContent = x.message; save.disabled = false; }
      });
      editBox.replaceChildren(h('section', null, h('h3', null, 'Senaryoyu düzenle: ' + sc.name), h('div', { class: 'panel' }, form)));
      editBox.scrollIntoView({ block: 'start', behavior: 'smooth' });
    };
    const rows = list.map((sc) => h('tr', { class: 'click', 'data-search': [sc.asset, sc.name, sc.last_error].join(' ').toLowerCase(), 'data-kind': sc.kind === 'browser' ? 'browser' : 'http', 'data-res': sc.last_ok == null ? 'wait' : sc.last_ok ? 'ok' : 'fail', onclick: () => showRuns(sc).catch((x) => { msg.className = 'msg error'; msg.textContent = x.message; }) },
      h('td', null, h('b', null, sc.name), ' ', pill('info', sc.kind === 'browser' ? 'Tarayıcı' : 'HTTP'), sc.last_changed_at && Date.now() - new Date(sc.last_changed_at) < 864e5 ? [' ', h('a', { href: '#', class: 'pill-link', title: 'Değişiklik ayrıntıları', onclick: (e) => { e.preventDefault(); e.stopPropagation(); showChanges(sc); } }, pill('warning', 'Sayfa değişti ' + ago(sc.last_changed_at)))] : null,
        sc.auth && sc.auth.blocked ? [' ', pill('down', 'Giriş durduruldu')] : sc.auth && sc.auth.login_fails ? [' ', pill('warning', `Giriş hatası ${sc.auth.login_fails}/3`)] : null, h('div', { class: 'muted small' }, sc.asset)),
      h('td', { class: 'small' }, (sc.steps || []).map((st, i) => h('div', null, (i + 1) + '. ' + st.method + ' ' + st.name))),
      h('td', { class: 'mono' }, sc.interval_sec + ' sn'),
      h('td', { class: 'mono small' }, ago(sc.last_run)),
      h('td', null, sc.last_ok == null ? pill('unknown', 'Bekliyor') : sc.last_ok ? pill('up', 'Başarılı') : h('span', { class: 'error small' }, sc.last_error || 'Başarısız'),
        sc.last_duration_ms != null ? h('div', { class: 'mono small muted' }, fmt(sc.last_duration_ms) + ' ms') : null),
      h('td', null, h('span', { class: 'row' },
        sc.auth && sc.auth.blocked ? h('button', { type: 'button', class: 'primary', onclick: (ev) => { ev.stopPropagation(); retryForm(sc); } }, 'Girişi yeniden dene') : null,
        h('button', { type: 'button', onclick: async (ev) => { ev.stopPropagation(); ev.target.disabled = true; msg.className = 'msg'; msg.textContent = 'Çalıştırılıyor…';
          try { const res = await api('/api/scenarios/' + sc.id + '/run', { method: 'POST' }); msg.textContent = ''; out.replaceChildren(stepResults(res)); showRuns(sc); }
          catch (x) { msg.className = 'msg error'; msg.textContent = x.message; } ev.target.disabled = false; } }, 'Şimdi çalıştır'),
        h('button', { type: 'button', onclick: (ev) => { ev.stopPropagation(); editForm(sc); } }, 'Düzenle'),
        h('button', { type: 'button', class: 'danger', onclick: async (ev) => { ev.stopPropagation(); if (!confirm('Senaryo silinsin mi?')) return;
          try { await api('/api/scenarios/' + sc.id, { method: 'DELETE' }); render(); } catch (x) { alert(x.message); } } }, 'Sil')))));
    return h('div', null,
      h('section', null, h('h2', null, 'Senaryolar (bot)'), capNote,
        h('p', { class: 'muted' }, 'İzlediğiniz uygulamada yapılan işlemleri (giriş, liste, ayrıntı…) sıralı HTTP adımları olarak tanımlayın; APP_MON bunları belirlenen aralıkla çalıştırır, her adımın süresini ve doğruluğunu ölçer, hata olursa alarm üretir. Çerezler adımlar arasında taşınır; bir adımın yanıtından alınan değer sonraki adımlarda {{ad}} ile kullanılır. Docker içinden ana makinedeki uygulamaya host.docker.internal ile erişin.'),
        h('p', { class: 'muted' }, 'Not: Başlıklar, gövde ve değişkenler (parola/token gibi) veritabanında düz metin saklanır ve arayüzde bir daha gösterilmez; mümkünse bu iş için ayrılmış, yetkisi sınırlı bir test kullanıcısı kullanın.'),
        h('section', null, h('h3', null, 'Tarayıcıdan kaydet'),
          h('p', { class: 'muted' }, 'Gerçek bir tarayıcıda (sunucuda çalışan, panelin içinde görünen) adımları siz yaparsınız; APP_MON kaydeder ve istediğiniz aralıkla yeniden oynatır. Her adımın süresini, sayfa yapısındaki değişiklikleri ve sayfanın eriştiği dış alan adlarını (topolojide görünür) ölçer.'),
          h('div', { class: 'panel' }, recorderPanel(assets, probesAll))),
        canWrite() ? await crawlSection(list) : null,
        h('details', { class: 'sect-details' }, h('summary', null, 'HTTP adımlarıyla elle tanımla'), h('div', { class: 'panel' }, f, msg), out),
        editBox),
      h('section', null, (() => {
        const sp = canWrite() && list.length ? selectable(rows, list.map((x) => x.id), 'senaryo') : null;
        const t = table((sp ? [''] : []).concat(['Senaryo', 'Adımlar', 'Aralık', 'Son çalışma', 'Son sonuç', '']), rows, 'Henüz senaryo tanımlanmadı.');
        const fb = list.length > 5 ? filterBar(FILTERS.senaryolar, [{ key: 'q', label: 'Ara (asset, senaryo, hata…)', type: 'text' },
          { key: 'kind', label: 'Tür', type: 'select', options: [['http', 'HTTP'], ['browser', 'Tarayıcı']] },
          { key: 'res', label: 'Sonuç', type: 'select', options: [['ok', 'Başarılı'], ['fail', 'Hatalı'], ['wait', 'Bekliyor']] }], t, list.length) : null;
        const bulk = sp ? sp.bar([bulkBtn('Etkinleştir', '/api/scenarios/bulk', sp.sel, { action: 'enable' }), bulkBtn('Durdur', '/api/scenarios/bulk', sp.sel, { action: 'disable' }),
          bulkSetButton('/api/scenarios/bulk', sp.sel, 30, false),
          bulkBtn('Sil', '/api/scenarios/bulk', sp.sel, { action: 'delete' }, '{n} senaryo silinsin mi? Bu işlem geri alınamaz.', 'danger')]) : null;
        return h('div', { class: 'stack' }, fb, bulk, t);
      })()),
      detail);
  }

  // Hazır alarm kural şablonları (metrik adları ingest/probe/senaryo tarafından üretilenlerle aynıdır).
  const RULE_TEMPLATES = [
    { group: 'Sunucu (agent)', items: [
      { name: 'CPU yüksek', metric: 'host.cpu_pct', comparator: '>', threshold: 90, window_sec: 300, severity: 'warning', desc: '5 dk ortalama CPU %90 üzerinde' },
      { name: 'CPU kritik', metric: 'host.cpu_pct', comparator: '>', threshold: 98, window_sec: 300, severity: 'critical', desc: '5 dk ortalama CPU %98 üzerinde' },
      { name: 'Bellek yüksek', metric: 'host.mem_pct', comparator: '>', threshold: 90, window_sec: 300, severity: 'warning', desc: '5 dk ortalama bellek %90 üzerinde' },
      { name: 'Disk dolmak üzere', metric: 'host.disk_pct', comparator: '>', threshold: 85, window_sec: 600, severity: 'warning', desc: 'Disk doluluğu %85 üzerinde' },
      { name: 'Disk kritik', metric: 'host.disk_pct', comparator: '>', threshold: 95, window_sec: 600, severity: 'critical', desc: 'Disk doluluğu %95 üzerinde' }] },
    { group: 'Güvenlik (saldırı yüzeyi)', items: [
      { name: 'Saldırı yüzeyi sapması', metric: 'surface.drift', comparator: '>', threshold: 0, window_sec: 120, severity: 'warning', desc: 'Referansta olmayan yeni port, uç nokta, TLS düşürme veya güvenlik başlığı kaybı' },
      { name: 'Destek dışı sürüm', metric: 'inventory.eol_count', comparator: '>', threshold: 0, window_sec: 3600, severity: 'warning', desc: 'Üreticinin güvenlik desteği bitmiş çalışma zamanı/servis sürümü var' },
      { name: 'Kritik zafiyet', metric: 'inventory.vuln_critical', comparator: '>', threshold: 0, window_sec: 3600, severity: 'critical', desc: 'Bağımlılıklarda kritik önemde bilinen zafiyet var' },
      { name: 'Yüksek zafiyet', metric: 'inventory.vuln_high', comparator: '>', threshold: 0, window_sec: 3600, severity: 'warning', desc: 'Bağımlılıklarda yüksek önemde bilinen zafiyet var' }] },
    { group: 'Erişilebilirlik ve hız (probe)', items: [
      { name: 'Servis erişilemiyor', metric: 'asset.down', comparator: '>', threshold: 0, window_sec: 30, severity: 'critical', desc: 'Hizmetin tüm probe/senaryoları başarısız' },
      { name: 'Servis kısmen erişilemiyor', metric: 'asset.degraded', comparator: '>', threshold: 0, window_sec: 30, severity: 'warning', desc: 'Sayfa açılıyor ama bazı alt bileşenlere erişilemiyor' },
      { name: 'Yavaş yanıt', metric: 'probe.latency_ms', comparator: '>', threshold: 1000, window_sec: 120, severity: 'warning', desc: 'Ortalama yanıt süresi 1 sn üzerinde' },
      { name: 'Çok yavaş yanıt', metric: 'probe.latency_ms', comparator: '>', threshold: 3000, window_sec: 120, severity: 'critical', desc: 'Ortalama yanıt süresi 3 sn üzerinde' },
      { name: 'Paket kaybı', metric: 'probe.packet_loss_pct', comparator: '>', threshold: 20, window_sec: 300, severity: 'warning', desc: 'ICMP paket kaybı %20 üzerinde' },
      { name: 'Sertifika süresi doluyor', metric: 'probe.cert_days_left', comparator: '<', threshold: 14, window_sec: 3600, severity: 'warning', desc: 'TLS sertifikasının bitimine 14 günden az' },
      { name: 'Sertifika kritik', metric: 'probe.cert_days_left', comparator: '<', threshold: 3, window_sec: 3600, severity: 'critical', desc: 'TLS sertifikasının bitimine 3 günden az' },
      { name: 'Saat sapması (NTP)', metric: 'probe.ntp_offset_ms', comparator: '>', threshold: 500, window_sec: 300, severity: 'warning', desc: 'NTP sunucusuna göre sapma 500 ms üzerinde' },
      { name: 'Aralıklı erişim sorunu', metric: 'probe.up', comparator: '<', threshold: 0.9, window_sec: 600, severity: 'warning', desc: '10 dk içinde başarı oranı %90 altında' }] },
    { group: 'Senaryo ve web sitesi', items: [
      { name: 'Senaryo başarısız', metric: 'scenario.up', comparator: '<', threshold: 1, window_sec: 60, severity: 'critical', desc: 'Senaryo bir adımda hata verdi' },
      { name: 'Yavaş senaryo', metric: 'scenario.duration_ms', comparator: '>', threshold: 5000, window_sec: 300, severity: 'warning', desc: 'Senaryo süresi 5 sn üzerinde' },
      { name: 'Sayfa yapısı değişti', metric: 'scenario.changes', comparator: '>', threshold: 0, window_sec: 300, severity: 'info', desc: 'Son iki kontrolde sayfa yapısı farklı (dinamik içerik elenir)' }] },
  ];
  const ruleKey = (r) => `${r.metric}|${r.comparator}|${r.threshold}`;
  const ruleBody = (r) => ({ name: r.name, metric: r.metric, comparator: r.comparator, threshold: r.threshold, window_sec: r.window_sec, severity: r.severity, asset_ids: r.asset_ids || [], group: r.group || '', tag: r.tag || '' });

  // Alarm panosu: sorunlu hizmetler önem derecesine göre renk ve şekille gösterilir.
  // Kritik = kırmızı sekizgen, Uyarı = sarı üçgen, Bilgi = mavi daire, Erişilemiyor = kırmızı çarpılı daire, Sağlıklı = yeşil daire.
  function sevShape(kind, size) {
    const sz = size || 28, a = { viewBox: '0 0 28 28', width: sz, height: sz, class: 'sv ' + kind, role: 'img', 'aria-label': ({ critical: 'Kritik', warning: 'Uyarı', info: 'Bilgi', down: 'Erişilemiyor', ok: 'Sağlıklı' })[kind] };
    const txt = (t) => s('text', { x: '14', y: '19', 'text-anchor': 'middle', class: 'sv-t' }, t);
    switch (kind) {
      case 'critical': return s('svg', a, s('polygon', { points: '9,2 19,2 26,9 26,19 19,26 9,26 2,19 2,9', class: 'sv-f' }), txt('!'));
      case 'warning': return s('svg', a, s('polygon', { points: '14,2 27,25 1,25', class: 'sv-f' }), s('text', { x: '14', y: '22', 'text-anchor': 'middle', class: 'sv-t' }, '!'));
      case 'down': return s('svg', a, s('circle', { cx: '14', cy: '14', r: '12', class: 'sv-f' }), s('path', { d: 'M9 9l10 10M19 9L9 19', class: 'sv-x' }));
      case 'ok': return s('svg', a, s('circle', { cx: '14', cy: '14', r: '12', class: 'sv-f' }), s('path', { d: 'M8.5 14.5l4 4 7-8', class: 'sv-x' }));
      default: return s('svg', a, s('circle', { cx: '14', cy: '14', r: '12', class: 'sv-f' }), txt('i'));
    }
  }
  function alarmBoard(open, assets, dash) {
    const rank = { critical: 3, down: 3, warning: 2, info: 1, ok: 0 };
    const by = new Map();
    for (const a of assets) by.set(a.name, { a, alerts: [], level: a.status === 'down' ? 'down' : a.status === 'degraded' ? 'warning' : 'ok' });
    for (const al of open) {
      let e = by.get(al.asset);
      if (!e) { e = { a: { name: al.asset, kind: 'service', status: 'unknown' }, alerts: [], level: 'ok' }; by.set(al.asset, e); }
      e.alerts.push(al);
      if (rank[al.severity] > rank[e.level]) e.level = al.severity;
    }
    const all = [...by.values()];
    const bad = all.filter((e) => e.level !== 'ok' || e.alerts.length).sort((x, y) => rank[y.level] - rank[x.level] || y.alerts.length - x.alerts.length || x.a.name.localeCompare(y.a.name));
    const good = all.filter((e) => !bad.includes(e));
    const worst = bad.length ? bad[0].level : 'ok';
    const cnt = (k) => bad.filter((e) => e.level === k).length;
    const banner = h('div', { class: 'ab-banner ' + (worst === 'down' ? 'critical' : worst) },
      sevShape(worst === 'down' ? 'critical' : worst, 44),
      h('div', null,
        h('b', { class: 'ab-title' }, bad.length ? `${bad.length} hizmette sorun var` : 'Tüm hizmetler sağlıklı'),
        h('div', { class: 'muted' }, bad.length ? `${cnt('critical') + cnt('down')} kritik · ${cnt('warning')} uyarı · ${cnt('info')} bilgi · ${good.length} hizmet sağlıklı` : `${all.length} hizmet izleniyor, açık alarm yok.`)));
    const tile = (e) => {
      const lvl = e.level;
      return h('div', { class: 'ab-tile ' + (lvl === 'down' ? 'critical' : lvl), tabindex: '0' },
        h('div', { class: 'ab-head' }, sevShape(lvl, 30),
          h('div', { class: 'ab-name' }, h('b', null, e.a.name), h('div', { class: 'muted small' }, (KIND[e.a.kind] || e.a.kind || '') + (e.a.status ? ' · ' + (STATUS[e.a.status] || e.a.status) : ''))),
          h('span', { class: 'ab-n' }, String(e.alerts.length || (lvl === 'down' ? '!' : '')))),
        e.alerts.length ? h('ul', { class: 'ab-list' }, e.alerts.slice(0, 4).map((al) => h('li', null,
          h('span', { class: 'ab-dot ' + al.severity }), h('span', null, al.rule), ' ',
          h('span', { class: 'mono small muted' }, `${fmt(al.value)} (${al.comparator} ${al.threshold}) · ${ago(al.opened_at)}`),
          al.ack_by ? h('span', { class: 'ab-ack' }, ' ✓ ' + al.ack_by) : null, al.silenced ? h('span', { class: 'ab-ack' }, ' ⏸ bakımda') : null)), e.alerts.length > 4 ? h('li', { class: 'muted small' }, `+${e.alerts.length - 4} alarm daha`) : null)
          : h('div', { class: 'muted small' }, e.a.status === 'degraded' ? 'Sayfa açılıyor ama bazı alt bileşenlere (probe/senaryo) erişilemiyor.' : 'Hizmet erişilemiyor.'));
    };
    const legend = h('div', { class: 'ab-legend' }, [['critical', 'Kritik'], ['down', 'Erişilemiyor'], ['warning', 'Uyarı'], ['info', 'Bilgi'], ['ok', 'Sağlıklı']].map(([k, l]) => h('span', null, sevShape(k, 16), ' ' + l)));
    let trend = null;
    if (dash && dash.alerts_critical && dash.alerts_critical.length) {
      const c = dash.alerts_critical, o = dash.alerts_other, n = c.length, mx = Math.max(1, ...c.map((v, i) => v + o[i]));
      const bw = 10, W = n * (bw + 3);
      const bars = [];
      for (let i = 0; i < n; i++) {
        const hc = (c[i] / mx) * 44, ho = (o[i] / mx) * 44;
        bars.push(s('rect', { x: String(i * (bw + 3)), y: String(48 - hc), width: String(bw), height: String(hc), class: 'tb critical' }));
        bars.push(s('rect', { x: String(i * (bw + 3)), y: String(48 - hc - ho), width: String(bw), height: String(ho), class: 'tb warning' }));
      }
      trend = h('div', { class: 'panel' }, h('h3', null, 'Son 24 saatte açılan alarmlar'),
        s('svg', { viewBox: `0 0 ${W} 50`, class: 'ab-trend', role: 'img', 'aria-label': 'Saatlik alarm sayısı', preserveAspectRatio: 'none' }, ...bars),
        h('div', { class: 'muted small' }, 'Kırmızı: kritik, sarı: diğer. Her çubuk bir saat.'));
    }
    return h('div', { class: 'stack' }, banner,
      bad.length ? h('div', { class: 'ab-grid' }, bad.map(tile)) : null,
      good.length ? h('div', { class: 'panel' }, h('h3', null, 'Sağlıklı hizmetler'),
        h('div', { class: 'ab-ok' }, good.map((e) => h('span', { class: 'ab-chip' }, sevShape('ok', 14), ' ' + e.a.name)))) : null,
      trend, legend);
  }

  const groupsOf = (assets) => [...new Set(assets.map((a) => a.group).filter(Boolean))].sort();
  const tagsOf = (assets) => [...new Set(assets.flatMap((a) => a.tags || []))].sort();
  function scopeText(r, assets) {
    const p = [];
    if (r.asset_ids && r.asset_ids.length) p.push(r.asset_ids.map((id) => (assets.find((a) => a.id === id) || { name: '#' + id }).name).join(', '));
    if (r.group) p.push('grup: ' + r.group);
    if (r.tag) p.push('etiket: ' + r.tag);
    return p.join(' · ');
  }
  const fmtDT = (iso) => new Date(iso).toLocaleString((window.LOCALE || 'tr-TR'), { dateStyle: 'short', timeStyle: 'short' });
  const toLocalInput = (d) => { const p = (n) => String(n).padStart(2, '0'); return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`; };

  function maintenanceSection(maint, assets) {
    const msg = h('p', { class: 'msg', role: 'status' });
    const now = new Date(), later = new Date(now.getTime() + 3600e3);
    const nm = h('input', { required: true, maxlength: '200', placeholder: 'ör. Aylık yama penceresi' });
    const st = h('input', { type: 'datetime-local', required: true, value: toLocalInput(now) });
    const en = h('input', { type: 'datetime-local', required: true, value: toLocalInput(later) });
    const rp = select(null, [['', 'Tek seferlik'], ['daily', 'Her gün'], ['weekly', 'Her hafta']]);
    const as = h('select', { multiple: true, size: '3' }, assets.map((a) => h('option', { value: String(a.id) }, a.name)));
    const gr = select(null, [['', 'Hepsi']].concat(groupsOf(assets).map((g) => [g, g])));
    const tg = h('input', { maxlength: '40', placeholder: 'boş = hepsi', list: 'tag-list' });
    const f = h('form', { class: 'inline' }, field('Ad', nm), field('Başlangıç', st), field('Bitiş', en), field('Tekrar', rp),
      field('Hizmetler (boş = hepsi)', as), field('Grup', gr), field('Etiket', tg), h('button', { type: 'submit', class: 'primary' }, 'Pencere ekle'));
    f.addEventListener('submit', async (e) => {
      e.preventDefault(); msg.className = 'msg'; msg.textContent = '';
      try {
        await api('/api/maintenance', { method: 'POST', body: { name: nm.value, starts_at: new Date(st.value).toISOString(), ends_at: new Date(en.value).toISOString(), repeat: rp.value,
          asset_ids: [...as.selectedOptions].map((o) => Number(o.value)), group: gr.value, tag: tg.value.trim() } });
        render();
      } catch (err) { msg.className = 'msg error'; msg.textContent = err.message; }
    });
    const nowT = Date.now();
    const state = (m) => {
      const a = new Date(m.starts_at).getTime(), b = new Date(m.ends_at).getTime();
      if (!m.repeat) return nowT < a ? 'planlı' : nowT <= b ? 'şu an aktif' : 'geçti';
      const per = (m.repeat === 'daily' ? 86400e3 : 604800e3);
      return nowT >= a && (nowT - a) % per <= b - a ? 'şu an aktif' : 'tekrarlıyor';
    };
    const rows = maint.map((m) => h('tr', null, h('td', null, h('b', null, m.name)), h('td', { class: 'mono small' }, fmtDT(m.starts_at) + ' → ' + fmtDT(m.ends_at)),
      h('td', null, ({ '': 'Tek seferlik', daily: 'Her gün', weekly: 'Her hafta' })[m.repeat]), h('td', { class: 'small' }, scopeText(m, assets) || 'Tüm hizmetler'),
      h('td', null, state(m) === 'şu an aktif' ? pill('warning', 'şu an aktif') : pill('unknown', state(m))),
      h('td', { class: 'actions' }, h('button', { type: 'button', class: 'danger', onclick: async () => { try { await api('/api/maintenance/' + m.id, { method: 'DELETE' }); render(); } catch (e) { alert(e.message); } } }, 'Sil'))));
    return h('section', null, h('h3', null, 'Bakım pencereleri'),
      h('p', { class: 'muted' }, 'Planlı bir çalışma sırasında (yama, yeniden başlatma) seçilen hizmetlerin alarmları açılır ama bildirim gönderilmez; alarmlarda "bakımda" etiketi görünür.'),
      table(['Ad', 'Zaman', 'Tekrar', 'Kapsam', 'Durum', ''], rows, 'Bakım penceresi yok.'),
      h('div', { class: 'panel stack' }, h('h3', null, 'Yeni bakım penceresi'), f, msg));
  }

  async function viewAlarmlar() {
    await refreshHeader();
    const [rules, open, resolved, assetsAll, dash, maint] = await Promise.all([api('/api/alert-rules'), api('/api/alerts?state=open'), api('/api/alerts?state=resolved'), api('/api/assets').catch(() => []), api('/api/dashboard').catch(() => null), api('/api/maintenance').catch(() => [])]);
    const msg = h('p', { class: 'msg', role: 'status' });
    let editing = null;
    const f = h('form', { class: 'inline' },
      field('Kural adı', h('input', { id: 'r-name', required: true, maxlength: '200' })),
      field('Metrik', h('input', { id: 'r-metric', required: true, placeholder: 'host.cpu_pct', maxlength: '128', list: 'r-metrics' })),
      h('datalist', { id: 'r-metrics' }, ['host.cpu_pct', 'host.mem_pct', 'host.disk_pct', 'asset.down', 'asset.degraded', 'probe.up', 'probe.latency_ms', 'probe.packet_loss_pct', 'probe.cert_days_left', 'probe.ntp_offset_ms', 'probe.snmp_value', 'probe.dns_answers', 'scenario.up', 'scenario.duration_ms', 'scenario.changes'].map((m) => h('option', { value: m }))),
      field('Koşul', select('r-cmp', [['>', 'büyükse (>)'], ['<', 'küçükse (<)']])),
      field('Eşik', h('input', { id: 'r-thr', type: 'number', step: 'any', required: true })),
      field('Pencere (sn)', h('input', { id: 'r-win', type: 'number', min: '10', value: '60' })),
      field('Önem', select('r-sev', [['warning', 'Uyarı'], ['critical', 'Kritik'], ['info', 'Bilgi']])),
      field('Kapsam: hizmetler (boş = hepsi)', h('select', { id: 'r-assets', multiple: true, size: '3' }, assetsAll.map((a) => h('option', { value: String(a.id) }, a.name)))),
      field('Kapsam: grup', select('r-group', [['', 'Hepsi']].concat(groupsOf(assetsAll).map((g) => [g, g])))),
      field('Kapsam: etiket', h('input', { id: 'r-tag', maxlength: '40', placeholder: 'boş = hepsi', list: 'tag-list' })),
      h('datalist', { id: 'tag-list' }, tagsOf(assetsAll).map((t) => h('option', { value: t }))),
      h('button', { type: 'submit', class: 'primary', id: 'r-save' }, 'Kural ekle'),
      h('button', { type: 'button', id: 'r-cancel', hidden: true }, 'Vazgeç'));
    const formTitle = h('h3', null, 'Yeni kural');
    const fill = (r) => {
      $('#r-name', f).value = r ? r.name : ''; $('#r-metric', f).value = r ? r.metric : '';
      $('#r-cmp', f).value = r ? r.comparator : '>'; $('#r-thr', f).value = r ? String(r.threshold) : '';
      $('#r-win', f).value = r ? String(r.window_sec) : '60'; $('#r-sev', f).value = r ? r.severity : 'warning';
      for (const o of $('#r-assets', f).options) o.selected = !!(r && r.asset_ids && r.asset_ids.includes(Number(o.value)));
      $('#r-group', f).value = r && r.group ? r.group : ''; $('#r-tag', f).value = r && r.tag ? r.tag : '';
      editing = r; formTitle.textContent = r ? `Kuralı düzenle: ${r.name}` : 'Yeni kural';
      $('#r-save', f).textContent = r ? 'Kaydet' : 'Kural ekle'; $('#r-cancel', f).hidden = !r;
      if (r) f.scrollIntoView({ block: 'center', behavior: 'smooth' });
    };
    $('#r-cancel', f).addEventListener('click', () => fill(null));
    f.addEventListener('submit', async (e) => {
      e.preventDefault();
      msg.className = 'msg'; msg.textContent = '';
      const body = { name: $('#r-name', f).value, metric: $('#r-metric', f).value, comparator: $('#r-cmp', f).value,
        threshold: Number($('#r-thr', f).value), window_sec: Number($('#r-win', f).value), severity: $('#r-sev', f).value,
        asset_ids: [...$('#r-assets', f).selectedOptions].map((o) => Number(o.value)), group: $('#r-group', f).value, tag: $('#r-tag', f).value.trim() };
      try {
        if (editing) await api('/api/alert-rules/' + editing.id, { method: 'PUT', body: { ...body, enabled: editing.enabled } });
        else await api('/api/alert-rules', { method: 'POST', body });
        render();
      } catch (err) { msg.className = 'msg error'; msg.textContent = err.message; }
    });
    const toggle = async (r) => { try { await api('/api/alert-rules/' + r.id, { method: 'PUT', body: { ...ruleBody(r), enabled: !r.enabled } }); render(); } catch (e) { alert(e.message); } };
    const rows = rules.map((r) => h('tr', { class: r.enabled ? '' : 'off' },
      h('td', null, h('b', null, r.name), r.enabled ? null : [' ', pill('unknown', 'kapalı')]), h('td', { class: 'mono small' }, `${r.metric} ${r.comparator} ${r.threshold}`, scopeText(r, assetsAll) ? h('div', { class: 'muted' }, 'kapsam: ' + scopeText(r, assetsAll)) : null),
      h('td', { class: 'mono' }, r.window_sec + ' sn'), h('td', null, pill(r.severity, SEV[r.severity])),
      h('td', { class: 'actions' },
        h('button', { type: 'button', onclick: () => fill(r) }, 'Düzenle'),
        h('button', { type: 'button', onclick: () => toggle(r) }, r.enabled ? 'Kapat' : 'Aç'),
        h('button', { type: 'button', class: 'danger', onclick: async () => { try { await api('/api/alert-rules/' + r.id, { method: 'DELETE' }); render(); } catch (e) { alert(e.message); } } }, 'Sil'))));

    // Şablon galerisi: zaten var olan (metrik+koşul+eşik aynı) kurallar "Ekli" görünür.
    const have = new Set(rules.map(ruleKey));
    const addTpl = async (list) => {
      msg.className = 'msg'; msg.textContent = '';
      try { for (const tpl of list) if (!have.has(ruleKey(tpl))) await api('/api/alert-rules', { method: 'POST', body: ruleBody(tpl) }); render(); }
      catch (err) { msg.className = 'msg error'; msg.textContent = err.message; }
    };
    const gallery = h('div', { class: 'stack' }, RULE_TEMPLATES.map((g) => h('div', { class: 'panel stack' },
      h('div', { class: 'row' }, h('h3', null, g.group),
        g.items.every((t) => have.has(ruleKey(t))) ? h('span', { class: 'muted small' }, 'Hepsi ekli') : h('button', { type: 'button', onclick: () => addTpl(g.items) }, 'Gruptaki hepsini ekle')),
      h('div', { class: 'tpls' }, g.items.map((t) => h('div', { class: 'tpl' },
        h('div', null, h('b', null, t.name), ' ', pill(t.severity, SEV[t.severity])),
        h('div', { class: 'muted small' }, t.desc), h('div', { class: 'mono small muted' }, `${t.metric} ${t.comparator} ${t.threshold} · ${t.window_sec} sn`),
        h('div', { class: 'btns' },
          have.has(ruleKey(t)) ? h('span', { class: 'muted small' }, 'Ekli') : h('button', { type: 'button', onclick: () => addTpl([t]) }, 'Ekle'),
          h('button', { type: 'button', onclick: () => fill({ ...t, id: null, enabled: true, _tpl: true }) || (editing = null, formTitle.textContent = 'Yeni kural (şablondan)', $('#r-save', f).textContent = 'Kural ekle', $('#r-cancel', f).hidden = false) }, 'Düzenleyip ekle'))))))));
    const sevN = (k) => open.filter((a) => a.severity === k).length;
    const aSum = h('div', { class: 'kpis' },
      kpi('Kritik', String(sevN('critical')), 'açık alarm', null, sevN('critical') ? 'bad' : ''),
      kpi('Uyarı', String(sevN('warning')), 'açık alarm', null, ''),
      kpi('Bilgi', String(sevN('info')), 'açık alarm', null, ''),
      kpi('Kapanan', String(resolved.length), 'son kayıtlar', null, ''),
      kpi('Kurallar', `${rules.filter((r) => r.enabled).length} / ${rules.length}`, 'etkin', null, ''));
    const sevDonut = open.length ? donutPanel('Açık alarmlar · önem', [{ label: 'Kritik', value: sevN('critical'), cls: 'crit' }, { label: 'Uyarı', value: sevN('warning'), cls: 'warn' }, { label: 'Bilgi', value: sevN('info'), cls: 'info' }], { label: 'Alarm önemi', sub: 'açık alarm' }) : null;
    return h('div', null,
      h('section', null, h('h2', null, 'Alarm panosu'), alarmBoard(open, assetsAll, dash)),
      h('section', null, h('h2', null, 'Açık alarmlar'), sevDonut ? h('div', { class: 'donut-row' }, sevDonut, aSum) : aSum, (() => { const ot = alertTable(open, false); return h('div', { class: 'stack' }, ruleChips(open, ot), ot); })()),
      h('section', null, h('h3', null, 'Alarm kuralları'),
        h('p', { class: 'muted' }, 'Kural, pencere içindeki ortalama değeri eşikle karşılaştırır. Kuralı düzenleyince açık alarmları kapanır; koşul sürüyorsa yeniden açılır.'),
        table(['Ad', 'Koşul', 'Pencere', 'Önem', ''], rows, 'Kural yok.'),
        h('div', { class: 'panel stack' }, formTitle, f, msg)),
      maintenanceSection(maint, assetsAll),
      h('section', null, h('h3', null, 'Hazır şablonlar'), h('p', { class: 'muted' }, 'Tek tıkla ekleyin ya da "Düzenleyip ekle" ile eşiği kendinize göre değiştirin.'), gallery),
      h('section', null, h('h3', null, 'Kapanan alarmlar'), alertTable(resolved, true)));
  }

  // ---- Bildirimler (yalnızca yönetici) ----
  const CH_SECRET = { pass: 'pass', client_secret: 'client_secret', token: 'token' };
  const CH_KINDS = {
    smtp: { label: 'E-posta (SMTP)', help: 'Her e-posta sunucusunda çalışır. Exchange kullanıyorsanız SMTP alıcı bağlayıcısı açık olmalıdır. Kimlik bilgileriniz şifresiz bağlantıda gönderilmesin diye STARTTLS veya SSL/TLS seçin.',
      fields: [['host', 'Sunucu', 'text', 'smtp.sirket.com'], ['port', 'Port (boş = otomatik)', 'number', '587'], ['security', 'Güvenlik', 'select', [['starttls', 'STARTTLS (587)'], ['ssl', 'SSL/TLS (465)'], ['none', 'Şifresiz (25) — önerilmez']]],
        ['user', 'Kullanıcı (isteğe bağlı)', 'text', ''], ['pass', 'Parola', 'secret', ''], ['from', 'Gönderen', 'text', 'APP_MON <izleme@sirket.com>'], ['to', 'Alıcılar (virgülle ayırın)', 'text', 'ekip@sirket.com, nobet@sirket.com'], ['skip_tls_verify', 'Sertifika doğrulamasını atla (kendinden imzalı sertifika)', 'check']] },
    graph: { label: 'Microsoft 365 / Exchange Online (Graph)', help: 'Azure\'da bir uygulama kaydı oluşturun, "Mail.Send" uygulama iznini verin ve yönetici onayı ekleyin. Gönderen, bu uygulamanın adına gönderebildiği bir posta kutusu olmalıdır. Modern kimlik doğrulama; MAPI\'nin bulut karşılığıdır.',
      fields: [['tenant_id', 'Dizin (tenant) kimliği', 'text', ''], ['client_id', 'Uygulama (client) kimliği', 'text', ''], ['client_secret', 'İstemci sırrı', 'secret', ''], ['from', 'Gönderen posta kutusu', 'text', 'izleme@sirket.com'], ['to', 'Alıcılar (virgülle ayırın)', 'text', '']] },
    ews: { label: 'Exchange Web Services (EWS)', help: 'Şirket içi Exchange sunucuları için. Yalnızca temel (Basic) kimlik doğrulama desteklenir; sunucunuz yalnızca NTLM/Kerberos kabul ediyorsa SMTP veya Graph kullanın.',
      fields: [['url', 'EWS adresi', 'text', 'https://mail.sirket.com/EWS/Exchange.asmx'], ['user', 'Kullanıcı', 'text', 'DOMAIN\\kullanici veya kullanici@sirket.com'], ['pass', 'Parola', 'secret', ''], ['to', 'Alıcılar (virgülle ayırın)', 'text', '']] },
    mapi: { label: 'MAPI (Outlook, yalnızca Windows)', help: 'Outlook\'un MAPI arayüzüyle gönderir. YALNIZCA APP_MON Windows\'ta doğrudan (Docker olmadan) çalışıyorsa ve aynı bilgisayarda Outlook kurulu, bir profil açıksa çalışır. Docker/Linux kurulumlarında SMTP, Graph veya EWS seçin.',
      fields: [['to', 'Alıcılar (virgülle ayırın)', 'text', ''], ['account', 'Outlook hesabı (boş = varsayılan)', 'text', 'izleme@sirket.com']] },
    webhook: { label: 'Webhook (genel JSON)', help: 'Adrese olay bilgisini JSON olarak POST eder (olay, önem, hizmet, kural, değer, zaman, bağlantı).', fields: [['url', 'Adres', 'text', 'https://ornek.com/hook']] },
    slack: { label: 'Slack', help: 'Slack\'te "Incoming Webhook" oluşturup adresini yapıştırın.', fields: [['url', 'Webhook adresi', 'text', 'https://hooks.slack.com/services/…']] },
    teams: { label: 'Microsoft Teams', help: 'Kanal için gelen webhook (veya Workflows) adresi.', fields: [['url', 'Webhook adresi', 'text', '']] },
    telegram: { label: 'Telegram', help: 'BotFather ile bir bot oluşturup anahtarını girin; sohbet kimliği botun yazacağı kişi veya grubun kimliğidir.', fields: [['token', 'Bot anahtarı', 'secret', ''], ['chat_id', 'Sohbet kimliği', 'text', '']] },
  };

  // ---- SIEM'e log iletimi ----
  const SIEM_PROTO = [['udp', 'Syslog UDP'], ['tcp', 'Syslog TCP'], ['tls', 'Syslog TLS'], ['http', 'HTTP (Splunk HEC / JSON)']];
  const SIEM_FMT = [['rfc5424', 'RFC 5424 (syslog)'], ['cef', 'CEF (ArcSight, QRadar, Sentinel)'], ['json', 'JSON']];
  function siemSection(list) {
    let editing = null;
    const msg = h('p', { class: 'msg', role: 'status' });
    const nameIn = h('input', { required: true, maxlength: '100', placeholder: 'ör. Merkezi SIEM' });
    const proto = select('s-proto', SIEM_PROTO, 'udp');
    const fmt = select('s-fmt', SIEM_FMT, 'rfc5424');
    const host = h('input', { placeholder: 'siem.sirket.local' });
    const port = h('input', { type: 'number', min: '1', max: '65535', placeholder: '514' });
    const url = h('input', { placeholder: 'https://splunk.sirket.local:8088/services/collector/event' });
    const auth = select('s-auth', [['none', 'Yok'], ['splunk', 'Splunk HEC token'], ['bearer', 'Bearer token']], 'none');
    const token = h('input', { type: 'password', autocomplete: 'new-password' });
    const app = h('input', { placeholder: 'appmon', maxlength: '48' });
    const fac = h('input', { type: 'number', min: '0', max: '23', value: '16' });
    const minSev = select('s-min', [['info', 'Bilgi ve üstü'], ['warning', 'Uyarı ve üstü'], ['critical', 'Yalnızca kritik']], 'info');
    const skip = h('input', { type: 'checkbox' });
    const audit = h('input', { type: 'checkbox' }); audit.checked = true;
    const alerts = h('input', { type: 'checkbox' }); alerts.checked = true;
    const enabled = h('input', { type: 'checkbox' }); enabled.checked = true;
    const netBox = h('div', { class: 'inline' }, field('Sunucu', host), field('Port (boş = varsayılan)', port));
    const httpBox = h('div', { class: 'inline' }, field('Adres (URL)', url), field('Kimlik doğrulama', auth), field('Token', token));
    const sync = () => { const http = proto.value === 'http'; netBox.hidden = http; httpBox.hidden = !http; fmt.disabled = http; };
    proto.addEventListener('change', sync);
    const save = h('button', { type: 'submit', class: 'primary' }, 'Hedefi ekle');
    const cancel = h('button', { type: 'button', hidden: true, onclick: () => fill(null) }, 'Vazgeç');
    const title = h('h3', null, 'Yeni SIEM hedefi');
    const f = h('form', { class: 'stack' },
      h('div', { class: 'inline' }, field('Ad', nameIn), field('Protokol', proto), field('Biçim', fmt)),
      netBox, httpBox,
      h('div', { class: 'inline' }, field('Uygulama adı (app-name)', app), field('Facility (0-23, 16 = local0)', fac), field('Alarmlar için en düşük önem', minSev)),
      h('div', { class: 'row' }, h('label', { class: 'check' }, audit, 'Denetim kayıtlarını gönder'), h('label', { class: 'check' }, alerts, 'Alarmları gönder (açılış / düzelme)'),
        h('label', { class: 'check' }, skip, 'TLS sertifika doğrulamasını atla (kendinden imzalı)'), h('label', { class: 'check' }, enabled, 'Etkin')),
      h('div', { class: 'row' }, save, cancel));
    const fill = (c) => {
      editing = c;
      const g = (c && c.config) || {};
      nameIn.value = c ? c.name : ''; proto.value = g.protocol || 'udp'; fmt.value = g.format || 'rfc5424'; host.value = g.host || ''; port.value = g.port || '';
      url.value = g.url || ''; auth.value = g.auth || 'none'; token.value = ''; token.placeholder = c && c.has && c.has.token ? 'değiştirmek için yazın (boş = aynı)' : '';
      app.value = g.app_name || ''; fac.value = g.facility != null ? g.facility : 16; minSev.value = g.min_severity || 'info'; skip.checked = !!g.skip_tls_verify;
      audit.checked = c ? !!g.audit : true; alerts.checked = c ? !!g.alerts : true; enabled.checked = c ? c.enabled : true;
      sync(); save.textContent = c ? 'Kaydet' : 'Hedefi ekle'; cancel.hidden = !c; title.textContent = c ? 'SIEM hedefini düzenle: ' + c.name : 'Yeni SIEM hedefi';
      if (c) f.scrollIntoView({ block: 'center', behavior: 'smooth' });
    };
    sync();
    f.addEventListener('submit', async (e) => {
      e.preventDefault(); msg.className = 'msg'; msg.textContent = '';
      const config = { protocol: proto.value, format: fmt.value, app_name: app.value.trim(), facility: Number(fac.value || 0), min_severity: minSev.value,
        skip_tls_verify: skip.checked, audit: audit.checked, alerts: alerts.checked, token: token.value };
      if (proto.value === 'http') Object.assign(config, { url: url.value.trim(), auth: auth.value });
      else Object.assign(config, { host: host.value.trim(), port: port.value ? Number(port.value) : 0 });
      const body = { name: nameIn.value, config, enabled: enabled.checked };
      try {
        if (editing) await api('/api/siem-targets/' + editing.id, { method: 'PUT', body });
        else await api('/api/siem-targets', { method: 'POST', body });
        render();
      } catch (err) { msg.className = 'msg error'; msg.textContent = err.message; }
    });
    const label = (c) => { const g = c.config; return (SIEM_PROTO.find((x) => x[0] === g.protocol) || [0, g.protocol])[1] + ' · ' + (g.protocol === 'http' ? g.url : g.host + ':' + (g.port || '')); };
    const cards = list.map((c) => {
      const out = h('span', { class: 'muted small' });
      const test = h('button', { type: 'button', onclick: async () => {
        test.disabled = true; out.className = 'muted small'; out.textContent = 'Gönderiliyor…';
        try {
          const r = await api(`/api/siem-targets/${c.id}/test`, { method: 'POST' });
          out.className = r.ok ? 'ok small' : 'error small'; out.textContent = r.ok ? 'Deneme olayı gönderildi.' : 'Başarısız: ' + r.error;
        } catch (e) { out.className = 'error small'; out.textContent = e.message; }
        test.disabled = false;
      } }, 'Deneme gönder');
      const g = c.config;
      return h('div', { class: 'ch-card' + (c.enabled ? '' : ' off') },
        h('div', { class: 'ch-head' }, h('span', { class: 'ch-ico webhook' }, 'S'), h('div', null, h('b', null, c.name), h('div', { class: 'muted small' }, label(c))),
          h('span', { class: 'spacer' }), c.enabled ? pill('up', 'Etkin') : pill('unknown', 'Kapalı')),
        h('div', { class: 'muted small' }, [g.format.toUpperCase(), g.audit ? 'denetim' : null, g.alerts ? 'alarmlar' : null].filter(Boolean).join(' · ')),
        c.last_error ? h('div', { class: 'error small' }, 'Son hata: ' + c.last_error) : (c.last_sent ? h('div', { class: 'muted small' }, 'Son gönderim: ' + ago(c.last_sent)) : h('div', { class: 'muted small' }, 'Henüz gönderim yok')),
        h('div', { class: 'row' }, test, h('button', { type: 'button', onclick: () => fill(c) }, 'Düzenle'),
          h('button', { type: 'button', onclick: async () => { try { await api('/api/siem-targets/' + c.id, { method: 'PUT', body: { name: c.name, config: c.config, enabled: !c.enabled } }); render(); } catch (e) { alert(e.message); } } }, c.enabled ? 'Kapat' : 'Aç'),
          h('button', { type: 'button', class: 'danger', onclick: async () => { if (!window.confirm(`"${c.name}" hedefi silinsin mi?`)) return; try { await api('/api/siem-targets/' + c.id, { method: 'DELETE' }); render(); } catch (e) { alert(e.message); } } }, 'Sil')),
        out);
    });
    return h('div', { class: 'stack' },
      h('section', null, h('h2', null, "SIEM'e log gönderimi"),
        h('p', { class: 'muted' }, "Denetim kayıtları ve alarmlar (açılış/düzelme) seçtiğiniz SIEM'e anlık iletilir. Syslog (UDP/TCP/TLS) veya HTTP (Splunk HEC, genel JSON) kullanılabilir; biçim RFC 5424, CEF veya JSON olabilir. Bakım penceresindeki alarmlar da iletilir (\"bakım penceresinde\" notuyla)."),
        cards.length ? h('div', { class: 'ch-grid' }, cards) : h('p', { class: 'muted' }, 'Henüz SIEM hedefi yok. Aşağıdan ekleyin.')),
      h('section', null, h('div', { class: 'panel stack' }, title, f, msg)));
  }

  async function viewBildirimler() {
    await refreshHeader();
    const list = await api('/api/notification-channels');
    const siemList = await api('/api/siem-targets');
    const msg = h('p', { class: 'msg', role: 'status' });
    let editing = null;
    const kindSel = select('n-kind', Object.entries(CH_KINDS).map(([k, v]) => [k, v.label]));
    const nameIn = h('input', { id: 'n-name', required: true, maxlength: '100', placeholder: 'ör. Nöbetçi ekip e-postası' });
    const minSel = select('n-min', [['info', 'Bilgi ve üstü'], ['warning', 'Uyarı ve üstü'], ['critical', 'Yalnızca kritik']], 'warning');
    const onRes = h('input', { type: 'checkbox' }); onRes.checked = true;
    const enabled = h('input', { type: 'checkbox' }); enabled.checked = true;
    const help = h('p', { class: 'muted small' });
    const box = h('div', { class: 'inline' });
    let inputs = {};
    const drawFields = (cfg, has) => {
      const spec = CH_KINDS[kindSel.value];
      help.textContent = spec.help;
      inputs = {};
      box.replaceChildren(...spec.fields.map(([key, label, type, ph]) => {
        let el;
        if (type === 'select') { el = select(null, ph, (cfg && cfg[key]) || ph[0][0]); }
        else if (type === 'check') { el = h('input', { type: 'checkbox' }); el.checked = !!(cfg && cfg[key]); inputs[key] = el; return h('label', { class: 'check' }, el, label); }
        else if (type === 'secret') { el = h('input', { type: 'password', autocomplete: 'new-password', placeholder: has && has[CH_SECRET[key]] ? 'değiştirmek için yazın (boş = aynı)' : ph }); }
        else { el = h('input', { type: type === 'number' ? 'number' : 'text', value: cfg && cfg[key] != null ? String(cfg[key]) : '', placeholder: ph || '' }); }
        inputs[key] = el;
        return field(label, el);
      }));
    };
    kindSel.addEventListener('change', () => drawFields(null, null));
    drawFields(null, null);
    const save = h('button', { type: 'submit', class: 'primary' }, 'Kanalı ekle');
    const cancel = h('button', { type: 'button', hidden: true, onclick: () => fill(null) }, 'Vazgeç');
    const title = h('h3', null, 'Yeni bildirim kanalı');
    const f = h('form', { class: 'stack' },
      h('div', { class: 'inline' }, field('Ad', nameIn), field('Tür', kindSel), field('Gönderilecek önem', minSel)),
      h('div', { class: 'row' }, h('label', { class: 'check' }, onRes, 'Düzelince de bildir'), h('label', { class: 'check' }, enabled, 'Etkin')),
      help, box, h('div', { class: 'row' }, save, cancel));
    const fill = (c) => {
      editing = c;
      kindSel.value = c ? c.kind : 'smtp'; nameIn.value = c ? c.name : ''; minSel.value = c ? c.min_severity : 'warning';
      onRes.checked = c ? c.on_resolve : true; enabled.checked = c ? c.enabled : true;
      drawFields(c ? c.config : null, c ? c.has : null);
      kindSel.disabled = !!c; save.textContent = c ? 'Kaydet' : 'Kanalı ekle'; cancel.hidden = !c; title.textContent = c ? 'Kanalı düzenle: ' + c.name : 'Yeni bildirim kanalı';
      if (c) f.scrollIntoView({ block: 'center', behavior: 'smooth' });
    };
    f.addEventListener('submit', async (e) => {
      e.preventDefault(); msg.className = 'msg'; msg.textContent = '';
      const config = {};
      for (const [k, el] of Object.entries(inputs)) {
        if (el.type === 'checkbox') config[k] = el.checked;
        else if (el.type === 'number') { if (el.value) config[k] = Number(el.value); }
        else config[k] = el.value.trim();
      }
      const body = { name: nameIn.value, kind: kindSel.value, config, min_severity: minSel.value, on_resolve: onRes.checked, enabled: enabled.checked };
      try {
        if (editing) await api('/api/notification-channels/' + editing.id, { method: 'PUT', body });
        else await api('/api/notification-channels', { method: 'POST', body });
        render();
      } catch (err) { msg.className = 'msg error'; msg.textContent = err.message; }
    });
    const cards = list.map((c) => {
      const out = h('span', { class: 'muted small' });
      const test = h('button', { type: 'button', onclick: async () => {
        test.disabled = true; out.className = 'muted small'; out.textContent = 'Gönderiliyor…';
        try {
          const r = await api(`/api/notification-channels/${c.id}/test`, { method: 'POST' });
          out.className = r.ok ? 'ok small' : 'error small'; out.textContent = r.ok ? 'Deneme mesajı gönderildi.' : 'Başarısız: ' + r.error;
        } catch (e) { out.className = 'error small'; out.textContent = e.message; }
        test.disabled = false;
      } }, 'Deneme gönder');
      return h('div', { class: 'ch-card' + (c.enabled ? '' : ' off') },
        h('div', { class: 'ch-head' }, h('span', { class: 'ch-ico ' + c.kind }, (CH_KINDS[c.kind] || {}).label ? CH_KINDS[c.kind].label.charAt(0) : '?'),
          h('div', null, h('b', null, c.name), h('div', { class: 'muted small' }, (CH_KINDS[c.kind] || {}).label || c.kind)),
          h('span', { class: 'spacer' }), c.enabled ? pill('up', 'Etkin') : pill('unknown', 'Kapalı')),
        h('div', { class: 'muted small' }, ({ info: 'Bilgi ve üstü', warning: 'Uyarı ve üstü', critical: 'Yalnızca kritik' })[c.min_severity] + (c.on_resolve ? ' · düzelince de bildirir' : '')),
        c.last_error ? h('div', { class: 'error small' }, 'Son hata: ' + c.last_error) : (c.last_sent ? h('div', { class: 'muted small' }, 'Son gönderim: ' + ago(c.last_sent)) : h('div', { class: 'muted small' }, 'Henüz gönderim yok')),
        h('div', { class: 'row' }, test, h('button', { type: 'button', onclick: () => fill(c) }, 'Düzenle'),
          h('button', { type: 'button', onclick: async () => { try { await api('/api/notification-channels/' + c.id, { method: 'PUT', body: { name: c.name, kind: c.kind, config: c.config, min_severity: c.min_severity, on_resolve: c.on_resolve, enabled: !c.enabled } }); render(); } catch (e) { alert(e.message); } } }, c.enabled ? 'Kapat' : 'Aç'),
          h('button', { type: 'button', class: 'danger', onclick: async () => { if (!window.confirm(`"${c.name}" kanalı silinsin mi?`)) return; try { await api('/api/notification-channels/' + c.id, { method: 'DELETE' }); render(); } catch (e) { alert(e.message); } } }, 'Sil')),
        out);
    });
    return h('div', null,
      h('section', null, h('h2', null, 'Bildirimler'),
        h('p', { class: 'muted' }, 'Alarm açıldığında ve düzeldiğinde seçtiğiniz kanallara otomatik mesaj gider. Bakım penceresindeki hizmetler için bildirim gönderilmez. Parolalar ve anahtarlar gizli saklanır, bu sayfada gösterilmez.'),
        cards.length ? h('div', { class: 'ch-grid' }, cards) : h('p', { class: 'muted' }, 'Henüz kanal yok. Aşağıdan ilk kanalı ekleyin.')),
      h('section', null, h('div', { class: 'panel stack' }, title, f, msg)),
      siemSection(siemList));
  }

  // ---- Sistem güncelleme (paket yükleyerek) ----
  // ---- Ajanlar: hafif Windows/Linux ajanı kurulum komutları ve kurulu ajanlar ----
  async function viewAjanlar() {
    await refreshHeader();
    const [files, assets, upd, acfg, rollout, fsum] = await Promise.all([api('/api/agent/files').catch(() => ({ enabled: false, files: [] })), api('/api/assets'), api('/api/agent/updates').catch(() => null), api('/api/agent/config').catch(() => null), api('/api/agent/rollout').catch(() => null), api('/api/agent/summary').catch(() => null)]);
    const root = h('div', { class: 'stack' });
    const msg = h('p', { class: 'msg', role: 'status' });
    const origin = location.origin;
    const f = { server: origin, key: '', name: '', watch: '', log: '', os: 'windows', arch: 'amd64' };
    const out = h('div', { class: 'stack' });
    const code = (t) => {
      const pre = h('pre', { class: 'cmd mono small' }, t);
      const b = h('button', { type: 'button', class: 'small', onclick: async (ev) => { try { await navigator.clipboard.writeText(t); ev.target.textContent = 'Kopyalandı'; } catch (x) { ev.target.textContent = 'Elle kopyalayın'; } } }, 'Kopyala');
      return h('div', { class: 'cmdbox' }, pre, b);
    };
    const have = (os, arch) => files.files.some((x) => x.os === os && x.arch === arch);
    const paint = () => {
      const q = (v) => '"' + String(v).replace(/"/g, '') + '"';
      const extra = (f.name ? ` -name ${q(f.name)}` : '') + (f.watch ? ` -watch ${q(f.watch)}` : '') + (f.log ? ` -log '${f.log.replace(/'/g, '')}'` : '');
      const key = f.key || '<AJAN-ANAHTARI>';
      const nm = `appmon-agent-${f.os}-${f.arch}` + (f.os === 'windows' ? '.exe' : '');
      const url = `${f.server.replace(/\/$/, '')}/agent/dl/${nm}`;
      const kids = [];
      if (!files.enabled || !have(f.os, f.arch)) kids.push(h('p', { class: 'error small' }, 'Bu sunucuda ' + nm + ' dosyası yok. Ajan dosyaları sunucu paketiyle (Docker imajı veya RHEL güncelleme paketi) gelir; sunucuyu güncelleyin.'));
      if (f.os === 'windows') {
        kids.push(h('p', { class: 'small muted' }, 'Hedef Windows makinede PowerShell\'i "Yönetici olarak" açıp çalıştırın. Ajan, açılışta SYSTEM olarak çalışan bir zamanlanmış görev olarak kurulur.'),
          code(`$d = Join-Path $env:TEMP 'appmon'; New-Item -ItemType Directory -Force $d | Out-Null\nInvoke-WebRequest '${url}' -OutFile "$d\\appmon-agent.exe"\n& "$d\\appmon-agent.exe" -install -server ${q(f.server)} -key ${q(key)}${extra}`),
          h('p', { class: 'small muted' }, 'Kaldırmak için: & "$env:ProgramFiles\\APP_MON Agent\\appmon-agent.exe" -uninstall. Sunucu HTTPS ve kurumsal/kendinden imzalı sertifika kullanıyorsa ajan komutuna -ca C:\\yol\\ca.pem ekleyin; indirme adımı için sertifika makinede güvenilir olmalıdır.'));
      } else {
        kids.push(h('p', { class: 'small muted' }, 'Hedef Linux makinede çalıştırın (systemd gerekir; root yetkisi). Ajan, kısıtlı bir systemd servisi olarak kurulur.'),
          code(`curl -fsSL -o /tmp/appmon-agent '${url}' && chmod +x /tmp/appmon-agent\nsudo /tmp/appmon-agent -install -server ${q(f.server)} -key ${q(key)}${extra}`),
          h('p', { class: 'small muted' }, 'Kaldırmak için: sudo appmon-agent -uninstall. Günlük: journalctl -u appmon-agent -f. Kendinden imzalı HTTPS için curl\'e -k yerine kurumsal CA kullanın ve ajana -ca /yol/ca.pem verin.'));
      }
      out.replaceChildren(...kids);
    };
    const inp = (label, k, ph, w) => h('label', { class: 'stack' }, h('span', { class: 'small muted' }, label),
      h('input', { type: 'text', placeholder: ph, value: f[k], oninput: (e) => { f[k] = e.target.value.trim(); paint(); }, class: w || '' }));
    const sel = (label, k, opts) => { const s2 = h('select', { onchange: (e) => { f[k] = e.target.value; paint(); } }, opts.map(([v, t]) => h('option', { value: v }, t))); s2.value = f[k]; return h('label', { class: 'stack' }, h('span', { class: 'small muted' }, label), s2); };
    const keyIn = h('input', { type: 'text', placeholder: 'apm_… (ajan rolünde anahtar)', oninput: (e) => { f.key = e.target.value.trim(); paint(); } });
    const mk = h('button', { type: 'button', onclick: async () => {
      try { const r = await api('/api/keys', { method: 'POST', body: { label: 'ajan-' + new Date().toISOString().slice(0, 16).replace('T', ' '), role: 'agent' } });
        f.key = r.key; keyIn.value = r.key; msg.textContent = 'Yeni ajan anahtarı oluşturuldu. Anahtar bir daha gösterilmez; komutu şimdi kullanın (Lisans ve anahtarlar sayfasından silebilirsiniz).'; paint(); } catch (e) { msg.textContent = e.message; msg.className = 'msg error'; }
    } }, 'Yeni ajan anahtarı oluştur');
    root.append(h('section', null, h('h2', null, 'Ajanlar'),
      h('p', { class: 'muted' }, 'Tek dosyalık, bağımlılıksız Windows ve Linux ajanı: CPU, bellek, takas, disk (her sürücü/bağlama noktası), ağ trafiği, süreç sayısı, açılış süresi ve izlenen süreçlerin çalışma durumu. Sunucuya ulaşamazsa ölçümleri tamponlar, bağlantı gelince zaman damgalarıyla gönderir. Ajan kurulan makine bir asset olarak otomatik eklenir (lisans sayısına dahildir).'),
      h('p', { class: 'muted small' }, 'Yapılandırma dosyası örnekleri (gönderilecek verileri # ile kapatabilirsiniz): ',
        h('a', { href: '/kurulum/appmon-agent-linux.conf', download: 'appmon-agent.conf' }, 'Linux (appmon-agent.conf)'), ' · ',
        h('a', { href: '/kurulum/appmon-agent-windows.conf', download: 'agent.conf' }, 'Windows (agent.conf)')),
      h('div', { class: 'panel stack' }, h('h3', null, 'Ajan kur'),
        h('div', { class: 'grid' }, sel('İşletim sistemi', 'os', [['windows', 'Windows'], ['linux', 'Linux']]), sel('Mimari', 'arch', [['amd64', 'x86-64 (amd64)'], ['arm64', 'ARM64']]),
          inp('Sunucu adresi (ajanın göreceği adres)', 'server', 'http://apm.firma.local'), inp('Asset adı (boşsa bilgisayar adı)', 'name', 'ör. ERP-DB-01'),
          inp('İzlenecek süreçler (virgülle, isteğe bağlı)', 'watch', 'ör. nginx,sshd veya sqlservr,w3wp'),
          inp('Günlük izleme (ad::dosya::regex, birden çoksa ;; ile)', 'log', 'ör. nginx_hata::/var/log/nginx/error.log::\\[error\\]')),
        h('p', { class: 'small muted' }, 'Günlük izleme: her turda dosyaya eklenen yeni satırlar sayılır; log.<ad>.count (eşleşen), log.<ad>.lines ve log.<ad>.readable metrikleri üretilir (örn. alarm kuralı: log.nginx_hata.count > 10). Log dosyaları genelde root\'a ait olduğundan Linux\'ta log izlemeli kurulumda ajan root olarak çalışır.'),
        h('div', { class: 'row' }, h('label', { class: 'stack grow' }, h('span', { class: 'small muted' }, 'Ajan anahtarı'), keyIn), mk), msg, out)));
    paint();
    const ag = assets.filter((a) => a.collection === 'agent');
    const um = {}; ((upd && upd.agents) || []).forEach((u) => { um[u.asset_id] = u; });
    const updMsg = h('p', { class: 'msg', role: 'status' });
    const askUpdate = async (body, text) => {
      try { const r = await api('/api/agent/update', { method: 'POST', body }); updMsg.textContent = text(r.updated); setTimeout(() => render(), 1200); } catch (e) { updMsg.textContent = e.message; }
    };
    const stCell = (a) => {
      const u = um[a.id];
      if (!u) return h('td', null, '—');
      if (!u.supported) return h('td', { class: 'small muted' }, 'Uzaktan güncelleme için ajanı bir kez elle güncelleyin');
      const kids = [];
      if (u.current) kids.push(pill('up', 'Güncel'));
      else if (u.available) kids.push(pill('degraded', u.requested ? 'Güncelleme bekliyor' : 'Yeni sürüm var'));
      else kids.push(h('span', { class: 'muted small' }, 'sunucuda dosya yok'));
      if (u.error) kids.push(' ', h('span', { class: 'small error', title: u.error }, 'Hata: ' + u.error));
      if (u.available && !u.requested) kids.push(' ', h('button', { type: 'button', class: 'small', onclick: () => askUpdate({ asset_ids: [a.id] }, () => 'Güncelleme istendi: ajan birkaç saniye içinde indirip yeniden başlar.') }, 'Güncelle'));
      if (u.requested) kids.push(' ', h('button', { type: 'button', class: 'small', onclick: () => askUpdate({ asset_ids: [a.id], cancel: true }, () => 'İstek geri alındı.') }, 'Vazgeç'));
      return h('td', null, kids);
    };
    const cm = {}; ((acfg && acfg.agents) || []).forEach((c) => { cm[c.asset_id] = c; });
    const cfCell = (a) => {
      const c = cm[a.id];
      if (!acfg || !c) return h('td', null, '—');
      if (!c.supported) return h('td', { class: 'small muted' }, 'Ajan 1.4.0 veya üstü gerekir');
      const st = c.locked ? h('span', { class: 'small muted' }, 'Yerel kilit (APM_REMOTE_CONFIG=off)')
        : c.remote ? (c.applied === c.version ? pill('up', 'Uzaktan · uygulandı') : pill('degraded', 'Uzaktan · bekliyor'))
          : h('span', { class: 'small muted' }, 'Yerel yapılandırma');
      return h('td', null, st, ' ', h('button', { type: 'button', class: 'small', disabled: c.locked, onclick: () => confDialog(a, c) }, 'Veri ayarı'));
    };
    const confDialog = (a, c, targets) => {
      const cur = new Set(c.remote ? c.send : c.reported);
      const boxes = new Map();
      const groups = [];
      for (const it of acfg.items) { let g = groups.find((x) => x.name === it.group); if (!g) { g = { name: it.group, items: [] }; groups.push(g); } g.items.push(it); }
      const mmsg = h('p', { class: 'msg', role: 'status' });
      const dbv = c.db || {};
      const dbIn = (label, val, attrs) => { const i = h('input', Object.assign({ type: 'text', value: val == null ? '' : String(val), autocomplete: 'off' }, attrs || {})); return [h('label', { class: 'stack' }, h('span', { class: 'small muted' }, label), i), i]; };
      const [dbHostL, dbHost] = dbIn('Sunucu', dbv.host || '127.0.0.1');
      const [dbPortL, dbPort] = dbIn('Port', dbv.port || 5432, { type: 'number', min: '1', max: '65535' });
      const [dbNameL, dbName] = dbIn('Veritabanı adı', dbv.name || '');
      const [dbUserL, dbUser] = dbIn('Kullanıcı', dbv.user || '');
      const [dbPassL, dbPass] = dbIn('Parola', '', { type: 'password', autocomplete: 'new-password', placeholder: dbv.password_set ? 'Kayıtlı (değiştirmek için yeni parola girin)' : '' });
      const dbSsl = select(null, [['require', 'sslmode: require'], ['verify-full', 'sslmode: verify-full'], ['verify-ca', 'sslmode: verify-ca'], ['disable', 'sslmode: disable']], dbv.sslmode || 'require');
      const dbRemove = h('input', { type: 'checkbox' });
      const secure = location.protocol === 'https:' || ['localhost', '127.0.0.1', '[::1]'].includes(location.hostname);
      const dbBox = h('fieldset', null, h('legend', null, 'Veritabanı bağlantısı (PostgreSQL, isteğe bağlı)'),
        h('p', { class: 'small muted' }, 'Girerseniz ajan bu bilgilerle veritabanına bağlanıp sorgu istatistiklerini gönderir (yukarıda "Veritabanı sorgu istatistikleri" işaretli olmalı). Parola Kasa ile şifrelenerek saklanır, bir daha gösterilmez ve ajana yalnızca HTTPS bağlantı üzerinden iletilir; ajan dosyayı yalnızca kendi hesabının okuyabildiği biçimde saklar. İzleme için yalnızca okuma yetkili (pg_monitor) bir kullanıcı kullanın. Yalnızca tek ajan için tanımlanır.'),
        secure ? null : h('p', { class: 'error small' }, 'Bu sayfa HTTPS ile açılmadığı için parola kaydedilemez. Sunucuya https:// adresiyle bağlanın.'),
        h('div', { class: 'grid' }, dbHostL, dbPortL, dbNameL, dbUserL, dbPassL, h('label', { class: 'stack' }, h('span', { class: 'small muted' }, 'Bağlantı güvenliği'), dbSsl)),
        dbv.host ? h('label', { class: 'check' }, dbRemove, ' Kayıtlı veritabanı bağlantısını kaldır') : null);
      const allAg = h('input', { type: 'checkbox' });
      const body = h('div', { class: 'stack' },
        h('p', { class: 'small muted' }, `İşaretli veriler sunucuya gönderilir. Kaydedince ajan ayarı bir sonraki gönderiminde alır, kalıcı olarak saklar ve servisi yeniden başlatır (birkaç saniye). Yerel dosyadaki SEND_* seçimlerinin yerine geçer; makinede APM_REMOTE_CONFIG=off varsa kabul edilmez. agent.up kalp atışı her zaman gider.`),
        groups.map((g) => h('fieldset', null, h('legend', null, g.name), g.items.map((it) => {
          const cb = h('input', { type: 'checkbox' }); cb.checked = cur.has(it.key); boxes.set(it.key, cb);
          return h('label', { class: 'check' }, cb, ' ', it.desc);
        }))),
        targets ? h('p', { class: 'small' }, 'Bu ayar seçilen ' + targets.length + ' ajana uygulanır (veritabanı bağlantısı toplu ayarlanamaz).') : dbBox,
        targets ? null : h('label', { class: 'check' }, allAg, ' Bu seçimi tüm ajanlara uygula (' + acfg.agents.filter((x) => x.supported && !x.locked).length + ')'),
        mmsg);
      const ids = () => (targets ? targets : allAg.checked ? acfg.agents.filter((x) => x.supported && !x.locked).map((x) => x.asset_id) : [a.id]);
      const save = h('button', { type: 'button', class: 'primary', onclick: async () => {
        const body = { asset_ids: ids(), send: [...boxes].filter(([, b]) => b.checked).map(([k]) => k) };
        if (!allAg.checked && !targets) {
          if (dbRemove.checked) body.db = { remove: true };
          else if (dbHost.value.trim() && dbName.value.trim() && dbUser.value.trim()) body.db = { host: dbHost.value.trim(), port: Number(dbPort.value) || 5432, name: dbName.value.trim(), user: dbUser.value.trim(), password: dbPass.value, sslmode: dbSsl.value };
        }
        try { const r = await api('/api/agent/config', { method: 'PUT', body }); mmsg.textContent = r.updated + ' ajan için ayar kaydedildi.'; setTimeout(() => { close(); render(); }, 900); } catch (e) { mmsg.textContent = e.message; }
      } }, 'Kaydet ve uygula');
      const clear = h('button', { type: 'button', onclick: async () => {
        try { await api('/api/agent/config', { method: 'PUT', body: { asset_ids: ids(), clear: true } }); mmsg.textContent = 'Uzaktan ayar kaldırıldı; ajan yerel yapılandırmaya döner.'; setTimeout(() => { close(); render(); }, 900); } catch (e) { mmsg.textContent = e.message; }
      } }, 'Yerel yapılandırmaya dön');
      body.append(h('div', { class: 'row' }, save, clear));
      const close = openModal('Veri ayarı: ' + a.name, body, false, true);
    };
    const nAvail = ((upd && upd.agents) || []).filter((u) => u.available && !u.requested).length;
    const autoBox = h('input', { type: 'checkbox' }); autoBox.checked = !!(upd && upd.auto);
    autoBox.addEventListener('change', async () => {
      try { await api('/api/agent/auto-update', { method: 'PUT', body: { enabled: autoBox.checked } }); updMsg.textContent = autoBox.checked ? 'Otomatik güncelleme açıldı: sunucudaki ajan dosyası değişince ajanlar kendini günceller.' : 'Otomatik güncelleme kapatıldı.'; } catch (e) { autoBox.checked = !autoBox.checked; updMsg.textContent = e.message; }
    });
    // ---- Ajan listesi: arama, süzgeç, sayfalama (daha fazla göster), çoklu seçim ve toplu işlemler (500+ ajan için) ----
    const AF = viewAjanlar.state || (viewAjanlar.state = { q: '', st: '', ver: '', cfg: '', grp: '', limit: 100 });
    const selA = new Set();
    const verOf = (a) => { const u = um[a.id]; return !u ? 'unknown' : !u.supported ? 'old' : u.current ? 'current' : u.available ? 'avail' : 'nofile'; };
    const cfgOf = (a) => { const c = cm[a.id]; return !c ? 'unknown' : !c.supported ? 'old' : c.locked ? 'locked' : c.remote ? (c.applied === c.version ? 'remote' : 'wait') : 'local'; };
    const groups = [...new Set(ag.map((a) => a.group).filter(Boolean))].sort();
    const matches = () => { const q = AF.q.trim().toLowerCase(); return ag.filter((a) => (!q || [a.name, (a.labels || {}).os, (a.labels || {}).weblog, (a.labels || {}).agent_version].join(' ').toLowerCase().includes(q))
      && (!AF.st || a.status === AF.st) && (!AF.ver || verOf(a) === AF.ver) && (!AF.cfg || cfgOf(a) === AF.cfg) && (!AF.grp || a.group === AF.grp)); };
    const tbodyA = h('tbody');
    const info = h('span', { class: 'muted small', role: 'status' });
    const selInfo = h('span', { class: 'small', role: 'status' });
    const moreBtn = h('button', { type: 'button', onclick: () => { AF.limit += 100; paintA(); } }, 'Daha fazla göster');
    const mkRow = (a) => {
      const cb = h('input', { type: 'checkbox', 'aria-label': a.name + ' seç' }); cb.checked = selA.has(a.id);
      cb.addEventListener('change', () => { if (cb.checked) selA.add(a.id); else selA.delete(a.id); paintSel(); });
      return h('tr', null, h('td', null, cb), h('td', null, a.name), h('td', null, ((a.labels || {}).os || '?') + '/' + ((a.labels || {}).arch || '?')), h('td', { class: 'mono' }, (a.labels || {}).agent_version || 'eski sürüm'),
        h('td', null, a.last_seen ? ago(a.last_seen) : '—'), h('td', null, pill(a.status, STATUS[a.status] || a.status)), h('td', { class: 'small' }, (a.labels || {}).weblog || '—'), stCell(a), cfCell(a));
    };
    const paintSel = () => { selInfo.textContent = selA.size ? selA.size + ' ajan seçili' : ''; [bUpd, bCfg, bClr].forEach((b) => { b.disabled = !selA.size; }); };
    const paintA = () => {
      const m = matches(); const shown = m.slice(0, AF.limit);
      tbodyA.replaceChildren(...(shown.length ? shown.map(mkRow) : [h('tr', null, h('td', { class: 'empty', colspan: 9 }, 'Süzgece uyan ajan yok.'))]));
      info.textContent = m.length === ag.length ? `${ag.length} ajan` : `${m.length} / ${ag.length} ajan`;
      moreBtn.hidden = m.length <= shown.length; moreBtn.textContent = `Daha fazla göster (${shown.length} / ${m.length})`;
      bAll.textContent = `Eşleşenlerin tümünü seç (${m.length})`; bRoll.disabled = !m.length;
      paintSel();
    };
    const bAll = h('button', { type: 'button', onclick: () => { matches().forEach((a) => selA.add(a.id)); paintA(); } }, 'Eşleşenlerin tümünü seç');
    const bClr = h('button', { type: 'button', onclick: () => { selA.clear(); paintA(); } }, 'Seçimi temizle');
    const bUpd = h('button', { type: 'button', onclick: () => askUpdate({ asset_ids: [...selA] }, (n) => `${n} ajan için güncelleme istendi.`) }, 'Seçilenleri güncelle');
    const bCfg = h('button', { type: 'button', onclick: () => {
      const tg = ag.filter((a) => selA.has(a.id) && cm[a.id] && cm[a.id].supported && !cm[a.id].locked).map((a) => a.id);
      if (!tg.length) { updMsg.textContent = 'Seçilenlerde veri ayarı yapılabilen ajan yok (ajan 1.4.0+ gerekir, yerel kilit olmamalı).'; return; }
      confDialog({ name: tg.length + ' ajan' }, cm[tg[0]], tg);
    } }, 'Seçilenlere veri ayarı…');
    const batchIn = h('input', { type: 'number', min: '1', max: '200', value: '25', style: 'width:5em', 'aria-label': 'Dalga büyüklüğü' });
    const bRoll = h('button', { type: 'button', title: 'Eşleşen ajanlar dalgalar halinde güncellenir; bir dalga bitmeden sonrakine geçilmez', onclick: async () => {
      const ids = matches().map((a) => a.id);
      if (!confirm(`${ids.length} ajan, ${batchIn.value}'erli dalgalarla güncellensin mi? Güncel veya çevrimdışı ajanlar atlanır.`)) return;
      try { await api('/api/agent/rollout', { method: 'POST', body: { asset_ids: ids, batch: Number(batchIn.value) || 25 } }); updMsg.textContent = 'Kademeli güncelleme başladı.'; setTimeout(() => render(), 800); } catch (e) { updMsg.textContent = e.message; }
    } }, 'Eşleşenleri kademeli güncelle');
    const fctl = [
      h('input', { type: 'search', placeholder: 'Ara (ad, işletim sistemi, sürüm)', value: AF.q, 'aria-label': 'Ajanlarda ara', oninput: (e) => { AF.q = e.target.value; AF.limit = 100; paintA(); } }),
      select(null, [['', 'Durum: tümü']].concat(Object.entries(STATUS)), AF.st),
      select(null, [['', 'Güncelleme: tümü'], ['current', 'Güncel'], ['avail', 'Yeni sürüm var'], ['old', 'Uzaktan güncellemeyi bilmeyen'], ['nofile', 'Sunucuda dosya yok']], AF.ver),
      select(null, [['', 'Veri ayarı: tümü'], ['remote', 'Uzaktan · uygulandı'], ['wait', 'Uzaktan · bekliyor'], ['local', 'Yerel'], ['locked', 'Yerel kilit'], ['old', 'Ajan 1.4.0 öncesi']], AF.cfg)];
    if (groups.length) fctl.push(select(null, [['', 'Grup: tümü']].concat(groups.map((g) => [g, g])), AF.grp));
    const keys = ['q', 'st', 'ver', 'cfg', 'grp'];
    fctl.forEach((el, i) => { if (i) el.addEventListener('change', () => { AF[keys[i]] = el.value; AF.limit = 100; paintA(); }); });
    const roll = rollout && (rollout.state === 'running' || rollout.state === 'paused') ? rollout : null;
    const rollBox = !rollout ? null : h('div', { class: 'callout' + (rollout.state === 'paused' ? ' warn' : '') },
      h('b', null, 'Kademeli güncelleme: ' + ({ running: 'sürüyor', paused: 'duraklatıldı', done: 'tamamlandı', canceled: 'iptal edildi' }[rollout.state] || rollout.state)),
      h('p', { class: 'small' }, `Toplam ${rollout.total} ajan · güncellendi ${rollout.done} · hatalı ${rollout.failed} · atlandı (çevrimdışı) ${rollout.skipped} · sırada ${rollout.queued} · dalga ${rollout.wave_no} (${rollout.in_wave} bekliyor, dalga büyüklüğü ${rollout.batch})`),
      rollout.note ? h('p', { class: 'small' }, rollout.note) : null,
      h('div', { class: 'row' },
        roll ? (rollout.state === 'running' ? h('button', { type: 'button', onclick: async () => { await api('/api/agent/rollout/pause', { method: 'POST' }).catch((e) => { updMsg.textContent = e.message; }); render(); } }, 'Duraklat')
          : h('button', { type: 'button', class: 'primary', onclick: async () => { await api('/api/agent/rollout/resume', { method: 'POST' }).catch((e) => { updMsg.textContent = e.message; }); render(); } }, 'Devam ettir')) : null,
        roll ? h('button', { type: 'button', class: 'danger', onclick: async () => { if (!confirm('Kademeli güncelleme iptal edilsin mi? Henüz güncellenmeyen ajanların isteği geri alınır.')) return; await api('/api/agent/rollout/cancel', { method: 'POST' }).catch((e) => { updMsg.textContent = e.message; }); render(); } }, 'İptal et') : null,
        h('button', { type: 'button', onclick: () => render() }, 'Durumu yenile')));
    const sumBox = !fsum || !fsum.total ? null : h('div', { class: 'kpis' },
      kpi('Ajan', String(fsum.total), `çevrimiçi ${(fsum.by_status.up || 0) + (fsum.by_status.degraded || 0)} · çevrimdışı ${fsum.by_status.offline || 0}`, null, (fsum.by_status.offline || 0) ? 'warn' : ''),
      kpi('Güncelleme', String(fsum.update.current), `güncel · ${fsum.update.available} yeni sürüm bekliyor`, null, fsum.update.errors ? 'bad' : fsum.update.available ? 'warn' : ''),
      kpi('Veri ayarı', String(fsum.config.remote_applied), `uzaktan · ${fsum.config.remote_pending} bekliyor · ${fsum.config.local} yerel · ${fsum.config.locked} kilitli`, null, fsum.config.remote_pending ? 'warn' : ''),
      kpi('Sürümler', String(fsum.versions.length), fsum.versions.slice(0, 4).map((v) => v.version + ' ×' + v.count).join(' · '), null, ''));
    const offNote = !fsum ? null : h('p', { class: 'muted small' }, h('span', null, `Çevrimdışı eşiği: ${fsum.offline_after_sec} sn sessizlik (sunucuda APM_OFFLINE_AGENT_SEC). Sunucu yeniden başlayınca kısa bir ısınma süresince (APM_OFFLINE_WARMUP_SEC) ajanlar çevrimdışı işaretlenmez.`),
      fsum.warmup_left_sec > 0 ? h('span', null, ` Kalan ısınma süresi ${fsum.warmup_left_sec} sn.`) : null);
    root.append(h('section', null, h('h3', null, `Ajanla izlenen makineler (${ag.length})`), sumBox, offNote,
      upd && upd.enabled && ag.length ? h('div', { class: 'row' },
        h('button', { type: 'button', disabled: !nAvail, title: 'Hepsine aynı anda istek gider; çok sayıda ajan için "kademeli güncelle" önerilir', onclick: () => askUpdate({ all: true }, (n) => `${n} ajan için güncelleme istendi.`) }, nAvail ? `Tümünü güncelle (${nAvail})` : 'Tüm ajanlar güncel'),
        h('label', { class: 'check', title: 'Sunucudaki ajan dosyası ajandakinden farklıysa ajan kendiliğinden günceller' }, autoBox, 'Otomatik güncelle')) : null,
      updMsg, rollBox,
      ag.length ? h('div', { class: 'stack' },
        h('div', { class: 'filters' }, fctl, info),
        h('div', { class: 'row' }, bAll, bClr, bUpd, bCfg, upd && upd.enabled ? h('span', { class: 'row' }, bRoll, h('label', { class: 'small' }, 'Dalga: ', batchIn)) : null, selInfo),
        h('div', { class: 'panel tablewrap' }, h('table', null, h('thead', null, h('tr', null, ['', 'Asset', 'İşletim sistemi', 'Ajan sürümü', 'Son veri', 'Durum', 'Web günlüğü', 'Güncelleme', 'Veri ayarı'].map((t) => h('th', null, t)))), tbodyA)),
        moreBtn) : h('p', { class: 'muted' }, 'Henüz ajan kurulmadı.'),
      upd && upd.bundled_version ? h('p', { class: 'small muted' }, 'Bu sunucudaki ajan sürümü: ' + upd.bundled_version + ' (ajan sürümü sunucu sürümünden bağımsızdır; yalnızca ajan kodu değişince artar).') : null,
      upd && upd.enabled ? h('p', { class: 'small muted' }, 'Güncelleme: ajan yeni dosyayı bu sunucudan indirir, SHA-256\'sını doğrular, kendini değiştirir ve yeniden başlar; hata olursa eski ajan çalışmaya devam eder. Yalnızca v2.7.5 ve sonrası ajanlar uzaktan güncellenir. Çok sayıda ajan için "kademeli güncelle" ajanları dalgalar halinde günceller; bir dalgada hata olursa kuyruk durur.') : null));
    if (ag.length) paintA();
    root.append(h('section', null, h('h3', null, 'Antivirüs / EDR izinleri'),
      h('div', { class: 'callout warn' }, h('b', null, 'Uç noktada antivirüs veya EDR varsa ajan için şu izinleri verin'),
        h('p', { class: 'small' }, 'Ajan imzasız tek bir Go dosyasıdır ve sistem düzeyinde izleme yapar; güvenlik ürünleri bunu şüpheli sayıp engelleyebilir, karantinaya alabilir veya kurulumu bozabilir. Aşağıdakileri güvenlik ekibinizle önceden izin listesine (allow-list / exclusion) ekleyin; ajanın SHA-256 değeri aşağıdaki "Hazır ajan dosyaları" tablosundadır.'),
        h('h4', null, 'Windows (Defender, CrowdStrike, SentinelOne, Cortex vb.)'),
        h('ul', { class: 'small' },
          h('li', null, 'Dosya/süreç izni: C:\\Program Files\\APP_MON Agent\\appmon-agent.exe (SHA-256 ile) ve veri klasörü C:\\ProgramData\\appmon-agent. Güncelleme sırasında appmon-agent.old.exe oluşur ve exe değiştirilir; bu "kendini değiştiren dosya" olarak işaretlenebilir.'),
          h('li', null, 'Kurulumda tetiklenebilecek eylemler (yönetici hesabı gerekir): schtasks ile SYSTEM olarak çalışan "APP_MON Agent" zamanlanmış görevi oluşturma, powershell (görev ayarı) ve icacls (yapılandırma dosyası izinleri) çalıştırma.'),
          h('li', null, 'Çalışırken: netstat -ano komutunu çalıştırma (bağlantı haritası), süreç/servis/port listesi ve süreç komut satırı okuma (uygulama keşfi), web sunucusu günlük dosyalarını okuma (IIS: C:\\inetpub\\logs), 127.0.0.1:4318 üzerinde yerel dinleme (OTLP alıcısı).'),
          h('li', null, 'Ağ: yalnızca APP_MON sunucusuna giden HTTP/HTTPS (sunucu adresi ve portu). Gelen bağlantı gerekmez. TLS denetimi (SSL inspection) yapan proxy/güvenlik duvarı varsa kurumsal CA\'yı ajana -ca ile verin.'),
          h('li', null, 'Yayıncı imzası yoksa SmartScreen uyarı verebilir; kurumsal kod imzalama sertifikanızla dosyayı imzalamak en temiz çözümdür.')),
        h('h4', null, 'Linux (SELinux, Defender for Endpoint, CrowdStrike, Wazuh/auditd vb.)'),
        h('ul', { class: 'small' },
          h('li', null, 'Dosya izni: /usr/local/bin/appmon-agent, /etc/appmon-agent.conf, /etc/systemd/system/appmon-agent.service ve /var/lib/appmon-agent. Kurulum bir systemd birimi oluşturur; güncelleme sırasında root olarak ikili dosya değiştirilir (ExecStartPre).'),
          h('li', null, 'Uygulama keşfi ve bağlantı haritası için /proc altındaki süreç ve soket bilgileri okunur (/proc/*/fd, /proc/net/tcp). Tüm kullanıcıların süreçlerini görmek için ajanın root çalışması gerekir (kurulumda -root); /proc "hidepid" ile bağlanmışsa veya EDR /proc erişimini kısıtlıyorsa bu veriler eksik gelir.'),
          h('li', null, 'SELinux "enforcing" ise ajan engellenirse: ausearch -m avc -ts recent ile reddi görün, restorecon -v /usr/local/bin/appmon-agent çalıştırın, gerekirse yerel bir politika modülü yazın.'),
          h('li', null, 'Ağ: yalnızca APP_MON sunucusuna giden bağlantı; yerel OTLP alıcısı yalnızca 127.0.0.1:4318 dinler.')),
        h('p', { class: 'small' }, 'İzin vermek istemediğiniz özellikleri kapatabilirsiniz: bu sayfadaki "Veri ayarı" ile bağlantı haritası, uygulama keşfi, yerel OTLP alıcısı ve uzaktan güncelleme ayrı ayrı kapatılır; böylece EDR\'nin en çok tepki verdiği eylemler (netstat, süreç taraması, yerel port dinleme, kendini güncelleme) hiç çalışmaz.'))));
    root.append(h('section', null, h('h3', null, 'Web sunucusu erişim günlüğü'),
      h('div', { class: 'panel stack' },
        h('p', null, 'Ajan kurulu makinede nginx, Apache ve IIS erişim günlüklerini kendiliğinden bulur ve okur; uç nokta bazında istek sayısı, hata oranı ve gecikmeyi Performans → Uygulamalar sayfasına özet olarak gönderir (satır, IP ve sorgu dizesi gönderilmez). Kapatmak için ajanı -no-weblog ile kurun.'),
        h('p', { class: 'small muted' }, 'IIS günlüğü istek süresini (time-taken) zaten içerir. nginx ve Apache\'nin varsayılan günlüğünde süre yoktur; süre (p50/p95/p99) için ajan, istek süresini içeren ikinci bir günlük ekleyebilir:'),
        code('sudo appmon-agent -web-setup -dry-run   # ne yapacağını gösterir, hiçbir şeyi değiştirmez\nsudo appmon-agent -web-setup            # onay ister, ayarı ekler ve yeniden yükler\nsudo appmon-agent -web-undo             # geri alır'),
        h('div', { class: 'callout warn' }, h('b', null, 'Riskler — -web-setup çalıştırmadan önce okuyun'),
          h('ul', null,
            h('li', null, 'Üretimdeki web sunucusunun ayarına yeni bir dosya eklenir ve ayar yeniden yüklenir (reload; bağlantılar kesilmez). Yeniden yüklemeden önce ayar sınanır; hata çıkarsa dosya silinir ve hiçbir şey değişmez. Yine de önce üretim dışı bir makinede deneyin.'),
            h('li', null, 'Sanal sunucunun (server / VirtualHost) kendi access_log / CustomLog satırı varsa o sunucu otomatik kapsanmaz; ajan bu sunucuları listeler, ilgili satırı oralara sizin eklemeniz gerekir. Mevcut hiçbir ayar dosyası değiştirilmez.'),
            h('li', null, 'Her istek için ikinci bir günlük satırı yazılır (ek disk ve G/Ç). Dosya /var/log/appmon/access.log; günlük döndürülür, 3 gün tutulur.'),
            h('li', null, 'Yerel günlükte tam istek adresi (sorgu dizesi dahil) saklanır; APP_MON\'a yalnızca sorgusuz, normalleştirilmiş ({id}) özet gider.'),
            h('li', null, 'SELinux açıksa günlük dizinine httpd_log_t etiketi verilir. Root yetkisi gerekir; Windows/IIS için ayar gerekmez.'))))));
    root.append(h('section', null, h('h3', null, 'Hazır ajan dosyaları'),
      files.files.length ? h('div', { class: 'panel' }, h('table', null, h('thead', null, h('tr', null, ['Dosya', 'Boyut', 'SHA-256'].map((t) => h('th', null, t)))),
        h('tbody', null, files.files.map((x) => h('tr', null, h('td', null, h('a', { href: '/agent/dl/' + x.name }, x.name)), h('td', null, (x.size / 1048576).toFixed(1) + ' MB'), h('td', { class: 'mono small' }, x.sha256.slice(0, 16) + '…')))))) : h('p', { class: 'muted' }, files.enabled ? 'Ajan klasörü boş.' : 'Ajan indirme bu sunucuda kapalı (APM_AGENT_DIR tanımlı değil).')));
    return root;
  }

  // ---- Küçük modal yardımcıları, disk ayrıntısı ve Kasa ----
  function openModal(title, content, small, wide) {
    const close = () => { ov.remove(); document.removeEventListener('keydown', onKey, true); };
    const onKey = (e) => { if (e.key === 'Escape') { e.stopPropagation(); close(); } };
    const ov = h('div', { class: 'modal-ov', role: 'dialog', 'aria-label': title, onclick: close },
      h('div', { class: 'modal' + (small ? ' modal-sm' : '') + (wide ? ' modal-wide' : ''), onclick: (e) => e.stopPropagation() },
        h('div', { class: 'row' }, h('h3', null, title), h('button', { type: 'button', onclick: close, 'aria-label': 'Kapat' }, 'Kapat')), content));
    document.addEventListener('keydown', onKey, true);
    document.body.append(ov);
    return close;
  }
  const gb = (v) => (v == null ? '—' : v >= 1024 ? fmt(v / 1024) + ' TB' : fmt(v) + ' GB');
  const dur = (sec) => { if (sec == null) return '—'; const d = Math.floor(sec / 86400), hh = Math.floor((sec % 86400) / 3600), mm = Math.floor((sec % 3600) / 60); return (d ? d + ' g ' : '') + hh + ' sa ' + mm + ' dk'; };
  async function diskDialog(assetId, name) {
    const body = h('div', { class: 'modal-body' }, h('p', { class: 'muted' }, 'yükleniyor…'));
    openModal('Sunucu ayrıntısı · ' + name, body);
    try {
      const [d, hi] = await Promise.all([api(`/api/assets/${assetId}/disks`), api(`/api/assets/${assetId}/hostinfo`).catch(() => null)]);
      const parts = [];
      const mt = (hi && hi.metrics) || {};
      const pct = (v) => (v == null ? '—' : fmt(v) + '%');
      const sum = [];
      if (mt['host.cpu_pct'] != null) sum.push(['CPU', pct(mt['host.cpu_pct'])]);
      if (mt['host.mem_pct'] != null) sum.push(['RAM', pct(mt['host.mem_pct']) + (mt['host.mem_total_gb'] != null ? ` · ${fmt(mt['host.mem_used_gb'])} / ${fmt(mt['host.mem_total_gb'])} GB` : '')]);
      if (mt['host.swap_pct'] != null) sum.push(['Swap', pct(mt['host.swap_pct']) + (mt['host.swap_total_gb'] != null ? ` · ${fmt(mt['host.swap_used_gb'])} / ${fmt(mt['host.swap_total_gb'])} GB` : '')]);
      if (mt['host.uptime_sec'] != null) sum.push(['Çalışma süresi', dur(mt['host.uptime_sec'])]);
      if (mt['host.load1'] != null) sum.push(['Yük ortalaması (1/5/15 dk)', [mt['host.load1'], mt['host.load5'], mt['host.load15']].map((x) => (x == null ? '—' : fmt(x))).join(' / ')]);
      if (mt['host.conns'] != null) sum.push(['Açık bağlantı', String(Math.round(mt['host.conns'])) + (mt['host.conn_rtt_ms'] != null ? ` · ort. RTT ${fmt(mt['host.conn_rtt_ms'])} ms` : '')]);
      if (sum.length) parts.push(h('div', { class: 'hostsum' }, sum.map(([k, v]) => h('div', { class: 'hs-item' }, h('span', { class: 'muted small' }, k), h('b', { class: 'mono' }, v)))));
      if (d.disks.length) {
        const rows = d.disks.map((x) => {
          const v = x.pct, lvl = v == null ? '' : v >= 90 ? 'crit' : v >= 75 ? 'warn' : 'ok';
          const fill = h('span', { class: 'fill ' + lvl }); fill.style.width = (v == null ? 0 : Math.min(100, v)) + '%';
          return h('tr', null, h('td', { class: 'mono' }, x.mount), h('td', { class: 'dk-bar' }, h('span', { class: 'mtr-t' }, fill)),
            h('td', { class: 'mono' }, v == null ? '—' : fmt(v) + '%'), h('td', { class: 'mono small' }, gb(x.used_gb)), h('td', { class: 'mono small' }, gb(x.total_gb)));
        });
        parts.push(h('h4', null, 'Dizinler (bağlama noktaları)'), table(['Bağlama noktası', 'Doluluk', '%', 'Kullanılan', 'Toplam'], rows, ''));
      }
      if (hi && hi.addrs && hi.addrs.length) parts.push(h('h4', null, 'IP adresleri'), h('p', { class: 'mono small' }, hi.addrs.join(', ')));
      if (hi && hi.ports && hi.ports.length) parts.push(h('h4', null, 'Dinleyen portlar'), h('p', { class: 'mono small' }, hi.ports.join(', ')));
      if (hi && hi.conns && hi.conns.length) {
        const rows = hi.conns.map((c) => h('tr', null, h('td', null, c.dir === 'out' ? 'giden' : 'gelen'), h('td', { class: 'mono' }, c.peer), h('td', { class: 'mono' }, String(c.port)), h('td', { class: 'mono' }, String(c.conns)), h('td', { class: 'mono small' }, c.rtt_ms == null ? '—' : fmt(c.rtt_ms) + ' ms')));
        parts.push(h('h4', null, 'En yoğun bağlantılar'), table(['Yön', 'Karşı taraf', 'Port', 'Bağlantı', 'RTT'], rows, ''));
      }
      if (!parts.length) { body.replaceChildren(h('p', { class: 'muted' }, 'Son 30 dakikada sunucu verisi yok.')); return; }
      const at = (hi && hi.time) || d.time;
      parts.push(h('p', { class: 'muted small' }, 'Son ölçüm: ' + (at ? new Date(at).toLocaleString((window.LOCALE || 'tr-TR')) : '—') + '. Hangi verilerin toplanacağı Probe\'lar sayfasında SSH probe\'unun "Toplanacak veriler" alanından seçilir; GB, swap, yük ve bağlantı verileri SSH yönteminde gelir.'));
      body.replaceChildren(...parts);
    } catch (e) { body.replaceChildren(h('p', { class: 'error' }, e.message)); }
  }

  async function probeDialog(id, asset) {
    const body = h('div', { class: 'modal-body' }, h('p', { class: 'muted' }, 'yükleniyor…'));
    openModal('Probe ayrıntısı · ' + asset, body);
    try {
      const d = await api(`/api/probes/${id}/detail`);
      const parts = [];
      const st = d.stats24h || {};
      const msv = (v) => (v == null ? '—' : fmt(v) + ' ms');
      const sum = [
        ['Durum', d.enabled ? (d.last_ok == null ? 'henüz çalışmadı' : d.last_ok ? 'çalışıyor' : 'hata (' + d.fail_streak + ' ardışık)') : 'devre dışı'],
        ['Tür', d.type.toUpperCase()], ['Hedef', d.target], ['Aralık', d.interval_sec + ' sn'], ['Zaman aşımı', d.timeout_ms + ' ms'],
        ['Son çalışma', d.last_run ? new Date(d.last_run).toLocaleString((window.LOCALE || 'tr-TR')) : '—']];
      parts.push(h('div', { class: 'hostsum' }, sum.map(([k, v]) => h('div', { class: 'hs-item' }, h('span', { class: 'muted small' }, k), h('b', { class: 'mono small' }, v)))));
      if (d.last_error) parts.push(h('p', { class: 'error small' }, d.last_error));
      parts.push(h('h4', null, 'Son 24 saat (asset geneli)'),
        h('div', { class: 'hostsum' }, [['Erişilebilirlik', st.up_pct == null ? '—' : fmt(st.up_pct) + '%'], ['Kontrol sayısı', String(st.checks || 0)], ['Ort. gecikme', msv(st.avg_ms)], ['p95 gecikme', msv(st.p95_ms)], ['En yüksek gecikme', msv(st.max_ms)]]
          .map(([k, v]) => h('div', { class: 'hs-item' }, h('span', { class: 'muted small' }, k), h('b', { class: 'mono' }, v)))));
      if (d.detail.length) parts.push(h('h4', null, 'Son ölçümün ayrıntısı'), table(['Alan', 'Değer'], d.detail.map((x) => h('tr', null, h('td', null, x.k), h('td', { class: 'mono small' }, x.v))), ''));
      else parts.push(h('p', { class: 'muted' }, 'Bu probe için henüz ayrıntı kaydedilmedi; bir sonraki çalışmadan sonra dolar.'));
      const mk = Object.keys(d.metrics || {}).sort();
      if (mk.length) parts.push(h('h4', null, 'Son ölçümler (metrikler)'), table(['Metrik', 'Değer'], mk.map((k) => h('tr', null, h('td', { class: 'mono small' }, k), h('td', { class: 'mono' }, fmt(d.metrics[k])))), ''));
      parts.push(h('p', { class: 'muted small' }, 'Gecikme ve erişilebilirlik ölçümleri asset bazlıdır; aynı asset\'in birden çok probe\'u varsa özet hepsini kapsar. Ayrıntılar gizli bilgi (parola, topluluk adı, bağlantı dizesi) içermez.'));
      body.replaceChildren(...parts);
    } catch (e) { body.replaceChildren(h('p', { class: 'error' }, e.message)); }
  }

  function secretForm(sec, done) {
    const isNew = !sec;
    const msg = h('p', { class: 'msg small', role: 'status' });
    const name = h('input', { value: sec ? sec.name : '', maxlength: '80', required: true, placeholder: 'ör. rhel-izleme-anahtari' });
    const kind = select(null, [['ssh_key', 'SSH özel anahtarı'], ['password', 'Parola'], ['token', 'Token / API anahtarı']], sec ? sec.kind : 'ssh_key');
    kind.disabled = !isNew;
    const note = h('input', { value: sec ? sec.note : '', maxlength: '300', placeholder: 'isteğe bağlı (nerede kullanılıyor?)' });
    const val = h('textarea', { rows: '7', spellcheck: 'false', autocomplete: 'off', placeholder: isNew ? '-----BEGIN OPENSSH PRIVATE KEY-----\n...' : 'değiştirmek için yeni değeri yapıştırın (boş = aynı kalır)', required: isNew });
    val.classList.add('mono');
    const go = h('button', { type: 'submit', class: 'primary' }, isNew ? 'Kasaya ekle' : 'Kaydet');
    const hint = h('p', { class: 'muted small' });
    const syncHint = () => { hint.textContent = kind.value === 'ssh_key' ? 'Parolasız (ssh-keygen -t ed25519 -N "") özel anahtar. Değer şifreli saklanır ve bir daha gösterilmez.' : 'Değer şifreli saklanır ve bir daha gösterilmez.'; };
    kind.addEventListener('change', syncHint); syncHint();
    let close;
    const f = h('form', { class: 'stack', onsubmit: async (e) => {
      e.preventDefault(); go.disabled = true; msg.className = 'msg small'; msg.textContent = '';
      try {
        const body = { name: name.value, kind: kind.value, note: note.value };
        if (val.value) body.value = val.value;
        await api(isNew ? '/api/secrets' : '/api/secrets/' + sec.id, { method: isNew ? 'POST' : 'PUT', body });
        val.value = ''; close(); done();
      } catch (err) { msg.className = 'msg error small'; msg.textContent = err.message; go.disabled = false; }
    } }, field('Ad', name), field('Tür', kind), field('Not', note), field(isNew ? 'Değer' : 'Yeni değer (isteğe bağlı)', val), hint, msg, h('div', { class: 'row' }, h('span'), go));
    close = openModal(isNew ? 'Kasaya yeni kayıt' : 'Kaydı değiştir: ' + sec.name, f, false);
    name.focus();
  }
  function secretDelete(sec, done) {
    const useKey = !ME.account;
    const inp = h('input', { type: 'password', autocomplete: useKey ? 'off' : 'current-password', placeholder: useKey ? 'API anahtarınızı yeniden girin' : 'Parolanız', required: true });
    const msg = h('p', { class: 'msg small' });
    const go = h('button', { type: 'submit', class: 'danger' }, 'Onayla ve sil');
    let close;
    const f = h('form', { class: 'stack', onsubmit: async (e) => {
      e.preventDefault(); go.disabled = true; msg.textContent = '';
      try { await api('/api/secrets/' + sec.id, { method: 'DELETE', body: { password: useKey ? undefined : inp.value, api_key: useKey ? inp.value : undefined } }); close(); done(); }
      catch (err) { msg.className = 'msg error small'; msg.textContent = err.message; go.disabled = false; inp.select(); }
    } }, h('p', { class: 'small' }, `"${sec.name}" kalıcı olarak silinecek ve geri alınamaz. Onaylamak için ${useKey ? 'API anahtarınızı' : 'parolanızı'} girin.`), inp, msg, h('div', { class: 'row' }, h('span'), go));
    close = openModal('Kayıt silinsin mi?', f, true);
    inp.focus();
  }

  async function viewKasa() {
    await refreshHeader();
    const [st, list] = await Promise.all([api('/api/vault'), api('/api/secrets')]);
    const fact = (k, v) => h('div', { class: 'fact' }, h('span', null, k), h('b', null, v));
    const root = h('div', { class: 'stack' });
    const srcLabel = { env: 'Ortam değişkeni (APM_MASTER_KEY)', file: 'Anahtar dosyası', generated: 'Bu açılışta otomatik üretildi' }[st.source] || st.source;
    root.append(h('section', null, h('h2', null, 'Kasa'),
      h('p', { class: 'muted' }, 'SSH anahtarı gibi gizli değerler AES-256-GCM ile şifrelenerek saklanır. Ana anahtar veritabanında değil, sunucuda ayrı bir yerde durur; bu yüzden yalnızca veritabanı yedeği çalınsa bile değerler okunamaz. Değerler arayüzden bir daha gösterilmez; probe\'lar kasadan seçer.'),
      st.enabled ? h('div', { class: 'panel facts' }, fact('Durum', 'Açık'), fact('Anahtar kaynağı', srcLabel), fact('Anahtar parmak izi', st.key_id), st.path ? fact('Anahtar dosyası', st.path) : null, fact('Kayıt sayısı', String(st.secrets)))
        : h('div', { class: 'panel' }, h('p', { class: 'error' }, 'Kasa kapalı: ' + (st.error || 'ana anahtar bulunamadı')),
            h('p', { class: 'small' }, 'Açmak için sunucuda bir ana anahtar tanımlayın (ör. "openssl rand -base64 32 > /etc/appmon/master.key; chmod 640 /etc/appmon/master.key", ardından APM_MASTER_KEY_FILE ile gösterin) ve servisi yeniden başlatın.')),
      st.enabled && st.generated ? h('div', { class: 'panel' }, h('p', { class: 'error small' }, 'Ana anahtar bu sunucuda otomatik üretildi. Anahtar dosyasını (' + st.path + ') güvenli bir yere YEDEKLEYİN: kaybolursa kasadaki değerler hiçbir şekilde geri alınamaz.')) : null,
      st.mismatch > 0 ? h('div', { class: 'panel' }, h('p', { class: 'error small' }, `${st.mismatch} kayıt farklı bir ana anahtarla şifrelenmiş; bu kayıtlar çözülemez. Eski anahtarı geri koyun veya kayıtları yeniden girin.`)) : null));
    const rows = list.map((x) => h('tr', null,
      h('td', null, h('b', null, x.name), x.note ? h('div', { class: 'muted small' }, x.note) : null),
      h('td', null, x.kind_label), h('td', { class: 'mono small' }, x.in_use ? x.in_use + ' probe' : '—'),
      h('td', null, x.key_ok ? pill('up', 'Çözülebilir') : pill('down', 'Çözülemez')),
      h('td', { class: 'small muted' }, (x.created_by || '') + ' · ' + new Date(x.updated_at).toLocaleDateString((window.LOCALE || 'tr-TR'))),
      h('td', null, h('span', { class: 'row' }, h('button', { type: 'button', onclick: () => secretForm(x, render) }, 'Değiştir'),
        h('button', { type: 'button', class: 'danger', title: x.in_use ? 'Kullanımdaki kayıt silinemez' : '', disabled: x.in_use > 0, onclick: () => secretDelete(x, render) }, 'Sil')))));
    root.append(h('section', null, h('div', { class: 'row' }, h('h3', null, 'Kayıtlar'),
      h('button', { type: 'button', class: 'primary', disabled: !st.enabled, onclick: () => secretForm(null, render) }, 'Yeni kayıt')),
      table(['Ad', 'Tür', 'Kullanım', 'Durum', 'Son değişiklik', ''], rows, 'Kasada kayıt yok. SSH ile sunucu izlemek için bir özel anahtar ekleyin.')));
    return root;
  }

  async function viewGuncelleme() {
    await refreshHeader();
    const info = await api('/api/system/update');
    const fact = (k, v) => h('div', { class: 'fact' }, h('span', null, k), h('b', null, v));
    const msg = h('p', { class: 'msg', role: 'status' });
    const root = h('div', { class: 'stack' });
    const when = (t) => (t ? new Date(t).toLocaleString((window.LOCALE || 'tr-TR')) : '');
    const facts = h('div', { class: 'panel facts' },
      fact('Çalışan sürüm', info.version), fact('Sunucu mimarisi', info.arch),
      fact('Paket imzası', info.signature_required ? 'Zorunlu (yalnızca imzalı paketler kabul edilir)' : 'Zorunlu değil (imza anahtarı tanıtılmadı)'));
    root.append(h('section', null, h('h2', null, 'Sistem güncelleme'),
      h('p', { class: 'muted' }, 'Yeni sürüm paketini (appmon-guncelleme-vX.Y.Z.tar.gz) buradan yükleyerek APP_MON\'u güncelleyin. Veritabanı yedeği otomatik alınır, veriler ve ayarlar korunur; yeni sürüm açılmazsa eski sürüme otomatik dönülür. Güncelleme sırasında servis birkaç saniye yeniden başlar.'),
      facts));
    const st = info.status;
    if (st) {
      const cls = st.state === 'ok' || st.state === 'reverted' ? 'ok' : st.state === 'running' ? 'muted' : 'error';
      const label = { ok: 'Başarılı', running: 'Sürüyor', failed: 'Başarısız', rolled_back: 'Otomatik geri alındı', reverted: 'Önceki sürüme dönüldü' }[st.state] || st.state;
      root.append(h('section', null, h('h3', null, 'Son güncelleme'), h('div', { class: 'panel' },
        h('div', { class: 'row' }, h('b', null, `${st.from || '?'} → ${st.to || '?'}`), pill(st.state === 'ok' || st.state === 'reverted' ? 'up' : st.state === 'running' ? 'unknown' : 'down', label), h('span', { class: 'muted small' }, when(st.time))),
        st.message ? h('p', { class: cls + ' small' }, st.message) : null)));
    }
    if (!info.supported) {
      root.append(h('section', null, h('div', { class: 'panel' }, h('p', { class: 'error' }, info.reason))));
      return root;
    }
    const waitDone = async (prevTime, what) => {
      const box = h('div', { class: 'panel' }, h('p', null, (what || 'Güncelleme') + ' uygulanıyor… servis yeniden başlayacak, lütfen bekleyin (sayfayı kapatmayın).'));
      root.replaceChildren(h('section', null, h('h2', null, 'Sistem güncelleme'), box));
      for (let i = 0; i < 300; i++) {
        await new Promise((r) => setTimeout(r, 2000));
        let cur;
        try { cur = await api('/api/system/update'); } catch (e) { continue; } // yeniden başlarken bağlantı kopabilir
        const s = cur.status;
        if (s && s.time !== prevTime && s.state !== 'running' && !cur.pending) {
          const good = s.state === 'ok' || s.state === 'reverted';
          box.replaceChildren(h('p', { class: good ? 'ok' : 'error' }, good ? `${s.state === 'ok' ? 'Güncelleme tamamlandı' : 'Önceki sürüme dönüldü'}: ${s.from} → ${s.to}. Sayfa yenileniyor…` : (s.message || 'İşlem başarısız.')),
            good ? null : h('button', { type: 'button', onclick: () => location.reload() }, 'Tamam'));
          if (good) setTimeout(() => location.reload(), 2500);
          return;
        }
      }
      box.replaceChildren(h('p', { class: 'error' }, 'Güncelleme sonucu alınamadı. Sunucuda "systemctl status appmon" ve "journalctl -u appmon" ile kontrol edin.'));
    };
    const stagedBox = h('div');
    const showStaged = (s) => {
      if (!s) { stagedBox.replaceChildren(); return; }
      const same = s.version === info.version;
      stagedBox.replaceChildren(h('div', { class: 'panel stack' },
        h('h3', null, 'Yüklenen paket doğrulandı'),
        h('div', { class: 'facts' }, fact('Sürüm', `${info.version} → ${s.version}${same ? ' (aynı sürüm, yeniden kurulum)' : ''}`), fact('İmza', s.signed ? 'Doğrulandı' : 'İmzasız'), fact('Dosyalar', s.files.join(', ')), s.notes ? fact('Not', s.notes) : null),
        h('p', { class: 'muted small' }, 'Uygula\'ya basınca veritabanı yedeği alınır, yeni sürüm yüklenir ve servis yeniden başlar.'),
        h('div', { class: 'row' },
          h('button', { type: 'button', class: 'primary', onclick: async (e) => {
            if (!window.confirm(`APP_MON ${info.version} → ${s.version} güncellensin mi? Servis birkaç saniye yeniden başlayacak.`)) return;
            e.target.disabled = true;
            try { await api('/api/system/update/apply', { method: 'POST', body: { id: s.id } }); waitDone(st ? st.time : undefined); }
            catch (err) { msg.className = 'msg error'; msg.textContent = err.message; e.target.disabled = false; }
          } }, 'Güncellemeyi uygula'),
          h('button', { type: 'button', onclick: async () => { try { await api('/api/system/update', { method: 'DELETE' }); showStaged(null); msg.textContent = ''; } catch (err) { msg.className = 'msg error'; msg.textContent = err.message; } } }, 'İptal'))));
    };
    showStaged(info.staged);
    const file = h('input', { type: 'file', accept: '.gz,.tgz,application/gzip' });
    const bar = h('progress', { max: '100', value: '0', hidden: true });
    const go = h('button', { type: 'button', class: 'primary' }, 'Yükle ve doğrula');
    go.addEventListener('click', () => {
      const f = file.files[0];
      if (!f) { msg.className = 'msg error'; msg.textContent = 'Önce bir güncelleme paketi seçin.'; return; }
      msg.className = 'msg'; msg.textContent = ''; go.disabled = true; bar.hidden = false; bar.value = 0;
      const x = new XMLHttpRequest();
      x.open('POST', '/api/system/update');
      x.setRequestHeader('X-API-Key', key); x.setRequestHeader('Content-Type', 'application/gzip');
      x.upload.onprogress = (e) => { if (e.lengthComputable) bar.value = (e.loaded / e.total) * 100; };
      x.onload = () => {
        go.disabled = false; bar.hidden = true;
        let d = {}; try { d = JSON.parse(x.responseText); } catch (e) { /* yoksay */ }
        if (x.status === 401) { logout(); return; }
        if (x.status >= 200 && x.status < 300) { showStaged(d.staged); file.value = ''; msg.className = 'msg ok'; msg.textContent = 'Paket doğrulandı. Uygulamak için aşağıdaki düğmeyi kullanın.'; }
        else { msg.className = 'msg error'; msg.textContent = d.error || ('Yükleme başarısız (HTTP ' + x.status + ')'); }
      };
      x.onerror = () => { go.disabled = false; bar.hidden = true; msg.className = 'msg error'; msg.textContent = 'Bağlantı hatası: paket yüklenemedi.'; };
      x.send(f);
    });
    root.append(h('section', null, h('h3', null, 'Güncelleme paketi yükle'), h('div', { class: 'panel stack' },
      h('div', { class: 'row' }, file, go), bar, msg)), h('section', null, stagedBox));
    if (info.prev) {
      const rb = h('button', { type: 'button', class: 'danger' }, `Önceki sürüme dön (${info.prev.version})`);
      rb.addEventListener('click', async () => {
        if (!window.confirm(`APP_MON ${info.version} sürümünden ${info.prev.version} sürümüne dönülsün mü?\n\nYalnızca uygulama dosyası değişir; veritabanı olduğu gibi kalır (güncelleme öncesi yedek: ${info.prev.backup || 'yok'}). Servis birkaç saniye yeniden başlar.`)) return;
        rb.disabled = true;
        try { await api('/api/system/update/rollback', { method: 'POST', body: {} }); waitDone(st ? st.time : undefined, 'Geri dönüş'); }
        catch (err) { msg.className = 'msg error'; msg.textContent = err.message; rb.disabled = false; }
      });
      root.append(h('section', null, h('h3', null, 'Kararsız güncelleme mi?'), h('div', { class: 'panel stack' },
        h('p', { class: 'muted small' }, `Son güncellemeden önceki sürüm (${info.prev.version}) sunucuda saklanıyor. Yeni sürüm hatalı çalışıyorsa tek tıkla ona dönebilirsiniz. Veritabanı değişiklikleri yalnızca ekleyici olduğundan eski sürüm mevcut veriyle çalışır; sorun yaşarsanız yukarıdaki yedeği elle geri yükleyin.`), rb)));
    }
    return root;
  }

  // ---- Yardım (kullanım kılavuzu) ----
  function mdInline(t) {
    const out = [];
    const re = /(\*\*[^*]+\*\*|`[^`]+`|\*[^*\s][^*]*\*)/g;
    let last = 0, m;
    while ((m = re.exec(t))) {
      if (m.index > last) out.push(t.slice(last, m.index));
      const x = m[0];
      out.push(x[0] === '`' ? h('code', null, x.slice(1, -1)) : x.startsWith('**') ? h('b', null, x.slice(2, -2)) : h('em', null, x.slice(1, -1)));
      last = m.index + x.length;
    }
    if (last < t.length) out.push(t.slice(last));
    return out;
  }
  // Küçük Markdown çevirici: başlık, paragraf, madde (iç içe), numaralı liste, tablo, kod bloğu, > uyarı kutusu.
  function mdRender(src) {
    const root = h('div', { class: 'help-doc' });
    const lines = src.split('\n');
    let i = 0;
    const cells = (l) => l.trim().replace(/^\||\|$/g, '').split('|').map((x) => x.trim());
    const slug = (t) => 'h-' + t.toLowerCase().replace(/[^a-z0-9ığüşöç]+/gi, '-').replace(/^-|-$/g, '');
    const listItem = (l) => /^(\s*)(?:([-*])|(\d+)\.)\s+(.*)$/.exec(l);
    while (i < lines.length) {
      const l = lines[i];
      if (!l.trim()) { i++; continue; }
      let m;
      if (l.startsWith('```')) {
        const buf = []; i++;
        while (i < lines.length && !lines[i].startsWith('```')) { buf.push(lines[i]); i++; }
        i++;
        root.append(h('pre', { class: 'help-code' }, h('code', null, buf.join('\n'))));
        continue;
      }
      if ((m = /^(#{1,3})\s+(.*)$/.exec(l))) {
        const lvl = m[1].length;
        root.append(h('h' + lvl, { id: slug(m[2]), 'data-lvl': String(lvl) }, mdInline(m[2]))); i++; continue;
      }
      if (/^>\s?/.test(l)) {
        const buf = [];
        while (i < lines.length && /^>\s?/.test(lines[i])) { buf.push(lines[i].replace(/^>\s?/, '')); i++; }
        root.append(h('blockquote', { class: 'help-note' }, mdInline(buf.join(' '))));
        continue;
      }
      if (l.trim().startsWith('|') && /^\|?\s*:?-{3,}/.test(lines[i + 1] || '')) {
        const head = cells(l); i += 2;
        const rows = [];
        while (i < lines.length && lines[i].trim().startsWith('|')) { rows.push(cells(lines[i])); i++; }
        root.append(h('div', { class: 'table-wrap' }, h('table', null, h('thead', null, h('tr', null, head.map((c) => h('th', null, mdInline(c))))),
          h('tbody', null, rows.map((r) => h('tr', null, r.map((c) => h('td', null, mdInline(c)))))))));
        continue;
      }
      if (listItem(l)) {
        // Düz veya iç içe liste; girinti (2+ boşluk) alt liste demektir.
        const ordered = !!listItem(l)[3];
        const top = h(ordered ? 'ol' : 'ul', null);
        const stack = [{ indent: listItem(l)[1].length, el: top }];
        root.append(top);
        while (i < lines.length && (m = listItem(lines[i]))) {
          const ind = m[1].length;
          while (stack.length > 1 && ind < stack[stack.length - 1].indent) stack.pop();
          let cur = stack[stack.length - 1];
          if (ind > cur.indent) {
            const sub = h(m[3] ? 'ol' : 'ul', null);
            const lastLi = cur.el.lastElementChild;
            (lastLi || cur.el).append(sub);
            cur = { indent: ind, el: sub }; stack.push(cur);
          }
          cur.el.append(h('li', null, mdInline(m[4])));
          i++;
          // devam satırı (girintili, madde olmayan)
          while (i < lines.length && /^\s{2,}\S/.test(lines[i]) && !listItem(lines[i])) { cur.el.lastElementChild.append(' ', ...mdInline(lines[i].trim())); i++; }
        }
        continue;
      }
      i++;
      root.append(h('p', null, mdInline(l)));
    }
    return root;
  }
  async function viewYardim() {
    const res = await fetch('/kilavuz.md', { cache: 'no-cache' });
    if (!res.ok) throw new Error('Kılavuz yüklenemedi');
    const doc = mdRender(await res.text());
    const heads = [...doc.querySelectorAll('h2')];
    // Bölümlere ayır: her h2 ve altındaki öğeler bir <section> olur (arama için).
    const sections = [];
    const kids = [...doc.children];
    let cur = null;
    doc.textContent = '';
    kids.forEach((el) => {
      if (el.tagName === 'H2') { cur = h('section', { class: 'help-sec', id: 'sec-' + el.id }); sections.push({ el: cur, head: el }); doc.append(cur); }
      (cur || doc).append(el);
    });
    const links = [];
    const toc = h('nav', { class: 'help-toc', 'aria-label': 'İçindekiler' });
    const q = h('input', { type: 'search', class: 'help-search', placeholder: 'Kılavuzda ara…', 'aria-label': 'Kılavuzda ara' });
    toc.append(q);
    sections.forEach((sc) => {
      const a = h('a', { href: '#yardim', onclick: (e) => { e.preventDefault(); sc.el.scrollIntoView({ behavior: 'smooth', block: 'start' }); } }, sc.head.textContent);
      links.push({ a, sc }); toc.append(a);
    });
    const info = h('p', { class: 'muted small help-count' });
    const top = h('button', { type: 'button', class: 'help-top', title: 'Başa dön', onclick: () => window.scrollTo({ top: 0, behavior: 'smooth' }) }, '↑');
    q.addEventListener('input', () => {
      const t = q.value.trim().toLocaleLowerCase('tr');
      let n = 0;
      links.forEach(({ a, sc }) => {
        const hit = !t || sc.el.textContent.toLocaleLowerCase('tr').includes(t);
        sc.el.hidden = !hit; a.hidden = !hit; if (hit && t) n++;
      });
      info.textContent = t ? (n ? n + ' bölümde bulundu' : 'Sonuç yok') : '';
    });
    // Okunan bölümü içindekilerde vurgula.
    if ('IntersectionObserver' in window) {
      const io = new IntersectionObserver((es) => es.forEach((en) => {
        const l = links.find((x) => x.sc.el === en.target);
        if (l && en.isIntersecting) { links.forEach((x) => x.a.classList.remove('on')); l.a.classList.add('on'); }
      }), { rootMargin: '-10% 0px -80% 0px' });
      sections.forEach((sc) => io.observe(sc.el));
    }
    void heads;
    return h('div', { class: 'help', 'data-notr': '' }, h('aside', { class: 'help-side' }, toc, info), h('div', { class: 'help-main' }, window.LANG === 'en' ? h('p', { class: 'help-note' }, 'The user guide is available in Turkish only.') : null, doc), top);
  }

  async function viewLisans() {
    const o = await refreshHeader();
    const lic = o.license;
    const keys = await api('/api/keys');
    const lx = await api('/api/license').catch(() => null);
    const qd = lx && lx.quotas;
    const signed = lx && lx.signed;
    const licMsg = h('p', { class: 'msg small', role: 'status' });
    const licFile = h('input', { type: 'file', accept: '.lic,application/json' });
    const licGo = h('button', { type: 'button', class: 'primary' }, 'Lisans dosyasını yükle');
    licGo.addEventListener('click', async () => {
      const f = licFile.files[0];
      if (!f) { licMsg.className = 'msg error small'; licMsg.textContent = 'Önce bir .lic dosyası seçin.'; return; }
      licGo.disabled = true; licMsg.className = 'msg small'; licMsg.textContent = '';
      try {
        const r = await fetch('/api/license/file', { method: 'POST', headers: { 'X-API-Key': key, 'Content-Type': 'application/json' }, body: await f.text() });
        let d = {}; try { d = await r.json(); } catch (x) { /* yoksay */ }
        if (!r.ok) throw new Error(d.error || ('HTTP ' + r.status));
        licMsg.className = 'msg ok small'; licMsg.textContent = 'Lisans yüklendi.'; setTimeout(() => render(), 800);
      } catch (err) { licMsg.className = 'msg error small'; licMsg.textContent = err.message; licGo.disabled = false; }
    });
    const licPanel = h('div', { class: 'panel stack' },
      signed ? h('p', { class: 'ok small' }, `İmzalı lisans: ${signed.customer} · kimlik ${signed.id} · ${new Date(signed.issued_at).toLocaleDateString((window.LOCALE || 'tr-TR'))} tarihli`) : h('p', { class: 'muted small' }, 'Henüz imzalı lisans dosyası yüklenmedi; kotalar elle tanımlı.'),
      h('div', { class: 'row' }, licFile, licGo), licMsg,
      h('p', { class: 'muted small' }, 'Lisans dosyası (.lic) platform yöneticisi tarafından üretilir ve yalnızca bu kiracıya yüklenebilir. Yüklenince limitler dosyadan okunur ve elle değiştirilemez.'));
    const quotaPanel = qd ? h('div', { class: 'panel facts' },
      ...[['Probe', `${lx.probes_used} / ${qd.probe_limit || qd.asset_limit}`], ['Kullanıcı', `${lx.users_used} / ${qd.user_limit}`], ['En kısa tarama aralığı', qd.min_interval_sec + ' sn'],
        ['Saklama süresi', qd.retention_days ? qd.retention_days + ' gün' : 'sunucu varsayılanı'],
        ['Tarayıcı senaryoları', qd.allow_browser ? 'açık' : 'kapalı'], ['Keşfet', qd.allow_crawl ? 'açık' : 'kapalı'], ['SSH izleme', qd.allow_ssh ? 'açık' : 'kapalı']]
        .map(([k, v]) => h('div', { class: 'fact' }, h('span', null, k), h('b', null, v)))) : null;
    const msg = h('p', { class: 'msg', role: 'status' });
    const lf = h('form', { class: 'inline' },
      field('Asset limiti', h('input', { id: 'l-limit', type: 'number', min: '0', required: true, value: String(lic.asset_limit) })),
      field('Geçerlilik tarihi (boşsa süresiz)', h('input', { id: 'l-until', type: 'date', value: lic.valid_until ? lic.valid_until.slice(0, 10) : '' })),
      h('button', { type: 'submit', class: 'primary' }, 'Lisansı güncelle'));
    lf.addEventListener('submit', async (e) => {
      e.preventDefault();
      msg.className = 'msg'; msg.textContent = '';
      const until = $('#l-until', lf).value;
      try {
        await api('/api/license', { method: 'PUT', body: { asset_limit: Number($('#l-limit', lf).value), valid_until: until ? new Date(until + 'T23:59:59Z').toISOString() : null } });
        msg.className = 'msg ok'; msg.textContent = 'Lisans güncellendi.'; refreshHeader();
      } catch (err) { msg.className = 'msg error'; msg.textContent = err.message; }
    });

    const keyOut = h('div', { hidden: true });
    const kf = h('form', { class: 'inline' },
      field('Anahtar etiketi', h('input', { id: 'k-label', required: true, maxlength: '100', placeholder: 'ör. web-sunucu-01 agent' })),
      field('Rol', select('k-role', [['viewer', 'İzleyici (yalnızca görüntüler)'], ['operator', 'İşletmeci (izleme ayarlarını değiştirir)'], ['admin', 'Yönetici (her şey)'], ['agent', 'Agent (yalnızca veri gönderir)']])),
      h('button', { type: 'submit', class: 'primary' }, 'Anahtar oluştur'));
    kf.addEventListener('submit', async (e) => {
      e.preventDefault();
      try {
        const r = await api('/api/keys', { method: 'POST', body: { label: $('#k-label', kf).value, role: $('#k-role', kf).value } });
        keyOut.hidden = false;
        keyOut.replaceChildren(h('div', { class: 'keybox' },
          h('span', null, 'Bu anahtar yalnızca bir kez gösterilir:'), h('code', null, r.key),
          h('button', { type: 'button', onclick: async (ev) => {
            try { await navigator.clipboard.writeText(r.key); ev.target.textContent = 'Kopyalandı'; } catch (x) { ev.target.textContent = 'Elle kopyalayın'; }
          } }, 'Kopyala')));
        kf.reset();
      } catch (err) { keyOut.hidden = false; keyOut.replaceChildren(h('p', { class: 'error' }, err.message)); }
    });
    const krows = keys.map((k) => h('tr', null, h('td', null, h('b', null, k.label)),
      h('td', null, ROLE_LABEL[k.role] || k.role), h('td', { class: 'mono small' }, ago(k.created_at)),
      h('td', null, h('button', { type: 'button', class: 'danger', onclick: async () => {
        if (!window.confirm(`"${k.label}" anahtarı silinsin mi? Bu anahtarı kullanan agent'lar veri gönderemez.`)) return;
        try { await api('/api/keys/' + k.id, { method: 'DELETE' }); render(); } catch (e) { alert(e.message); }
      } }, 'Sil'))));

    const origin = location.origin;
    return h('div', { style: null },
      h('section', null, h('h2', null, 'Lisans'),
        h('div', { class: 'kpis' },
          kpi('Kullanılan asset', `${lic.assets_used} / ${lic.asset_limit}`, lic.asset_limit ? `%${Math.round(lic.assets_used / lic.asset_limit * 100)} dolu` : 'sınırsız', null, lic.asset_limit && lic.assets_used >= lic.asset_limit ? 'bad' : ''),
          kpi('Mod', lic.mode === 'saas' ? 'SaaS' : 'On-prem', 'dağıtım', null, ''),
          kpi('Geçerlilik', lic.valid_until ? lic.valid_until.slice(0, 10) : 'Süresiz', 'bitiş tarihi', null, '')),
        h('p', { class: 'muted' }, `${lic.mode === 'saas' ? 'SaaS' : 'On-premise'} modu. İzlenen her benzersiz asset (sunucu, servis, veritabanı, URL) lisanstan bir birim düşer; limit dolunca yeni asset kabul edilmez, mevcutlar izlenmeye devam eder.`),
        h('div', { class: 'stack' }, quotaPanel, licPanel, ME.platform && !signed ? h('div', { class: 'panel' }, lf, msg) : null)),
      h('section', null, h('h2', null, 'API anahtarları'), keyOut,
        table(['Etiket', 'Rol', 'Oluşturulma', ''], krows, 'Anahtar yok.'),
        h('div', { class: 'panel' }, kf)),
      h('section', null, h('h2', null, 'Veri kaynağı bağlama'),
        h('div', { class: 'grid' },
          h('div', { class: 'panel' }, h('h3', null, '1. Agent\'sız (tercih edilen)'),
            h('p', { class: 'muted' }, '.NET, Java, Python ve Go uygulamaları OpenTelemetry SDK ile metriği doğrudan sunucuya yollar. Sunucuya kurulum gerekmez.'),
            h('pre', null, `OTEL_EXPORTER_OTLP_ENDPOINT=${origin}\nOTEL_EXPORTER_OTLP_PROTOCOL=http/json\nOTEL_EXPORTER_OTLP_HEADERS=X-API-Key=<agent anahtarı>\nOTEL_SERVICE_NAME=odeme-servisi`),
            h('p', { class: 'muted small' }, 'Bu sürüm yalnızca OTLP/HTTP JSON kabul eder. Protobuf gönderen SDK\'lar için OpenTelemetry Collector (otlphttp, encoding: json) kullanın.')),
          h('div', { class: 'panel' }, h('h3', null, '2. Ajan (isteğe bağlı)'),
            h('p', { class: 'muted' }, 'CPU, bellek ve disk gibi ana makine metrikleri için tek dosyalık Go agent\'ı (Linux).'),
            h('pre', null, `APM_SERVER=${origin} \\\nAPM_KEY=<agent anahtarı> \\\n./appmon-agent`)))));
  }


  // ---- Ayarlar ve tema ----
  const PALETTES = [['default', 'Varsayılan', '#0b7a75', '#f4f7f8'], ['ocean', 'Okyanus', '#1f66c9', '#eef4fb'], ['forest', 'Orman', '#2e7d4f', '#f1f6f0'], ['sunset', 'Gün batımı', '#c2570c', '#faf4ef'], ['rose', 'Gül', '#b8325c', '#faf1f4'], ['graphite', 'Grafit', '#3f4654', '#f3f3f4'], ['lavender', 'Lavanta', '#6d4fd1', '#f5f2fb'], ['mint', 'Nane', '#0e9f85', '#eff8f5'], ['sand', 'Kum', '#a67c00', '#f8f4ea'], ['coral', 'Mercan', '#e0533d', '#fdf2f0'], ['nord', 'Nordik', '#5e81ac', '#eceff4'], ['plum', 'Erik', '#9b2f8a', '#f8eff6'], ['neon', 'Neon', '#c2187f', '#f2f4fb']];
  const ACCENTS = [['auto', 'Palete göre', ''], ['teal', 'Turkuaz', '#0b7a75'], ['blue', 'Mavi', '#2a6fd6'], ['violet', 'Mor', '#6250d6'], ['slate', 'Gri', '#475569'], ['orange', 'Turuncu', '#d9650b'], ['pink', 'Pembe', '#c9367a'], ['green', 'Yeşil', '#2f8f46'], ['red', 'Kırmızı', '#c62d2d'], ['cyan', 'Camgöbeği', '#0b87a8']];
  function getPref(k, def) { try { return localStorage.getItem(k) || def; } catch (e) { return def; } }
  function setPref(k, v) { try { localStorage.setItem(k, v); } catch (e) { /* depolama kapalı: yalnızca bu oturumda geçerli */ } }
  function applyTheme(t) {
    if (t === 'light' || t === 'dark') document.documentElement.setAttribute('data-theme', t); else document.documentElement.removeAttribute('data-theme');
  }
  function applyAccent(a) { if (a && a !== 'auto' && a !== 'teal') document.documentElement.setAttribute('data-accent', a); else document.documentElement.removeAttribute('data-accent'); }
  function applyLayout(l) { if (l === 'narrow') document.documentElement.setAttribute('data-layout', 'narrow'); else document.documentElement.removeAttribute('data-layout'); }
  function applyPalette(p) { if (p && p !== 'default') document.documentElement.setAttribute('data-palette', p); else document.documentElement.removeAttribute('data-palette'); }

  async function viewAyarlar() {
    await refreshHeader();
    const info = await api('/api/settings');
    let theme = getPref('apm_theme', 'system'), accent = getPref('apm_accent', 'auto'), palette = getPref('apm_palette', 'default'), layout = getPref('apm_layout', 'auto');
    const themeBox = h('div', { class: 'seg', role: 'group', 'aria-label': 'Tema' });
    const palBox = h('div', { class: 'seg', role: 'group', 'aria-label': 'Renk paleti' });
    const accentBox = h('div', { class: 'seg', role: 'group', 'aria-label': 'Vurgu rengi' });
    const layBox = h('div', { class: 'seg', role: 'group', 'aria-label': 'Sayfa düzeni' });
    const paint = () => {
      themeBox.replaceChildren(...[['system', 'Sistem'], ['light', 'Açık'], ['dark', 'Koyu']].map(([v, t]) =>
        h('button', { type: 'button', 'aria-pressed': String(theme === v), onclick: () => { theme = v; setPref('apm_theme', v); applyTheme(v); paint(); } }, t)));
      palBox.replaceChildren(...PALETTES.map(([v, t, c, bg]) => {
        const sw = h('span', { class: 'swatch pal' }); sw.style.background = c; sw.style.boxShadow = '0 0 0 4px ' + bg + ' inset';
        return h('button', { type: 'button', 'aria-pressed': String(palette === v), onclick: () => { palette = v; setPref('apm_palette', v); applyPalette(v); paint(); } }, sw, t);
      }));
      layBox.replaceChildren(...[['auto', 'Otomatik (ekran çözünürlüğüne uyar)'], ['narrow', 'Dar (ortalanmış, 1180 px)']].map(([v, t]) =>
        h('button', { type: 'button', 'aria-pressed': String(layout === v), onclick: () => { layout = v; setPref('apm_layout', v); applyLayout(v); paint(); } }, t)));
      accentBox.replaceChildren(...ACCENTS.map(([v, t, c]) => {
        const sw = h('span', { class: 'swatch' }); if (c) sw.style.background = c; else sw.hidden = true;
        return h('button', { type: 'button', 'aria-pressed': String(accent === v), onclick: () => { accent = v; setPref('apm_accent', v); applyAccent(v); paint(); } }, sw, t);
      }));
    };
    paint();
    const fact = (k, v) => h('div', { class: 'fact' }, h('span', null, k), h('b', null, v));
    const statusSec = ME.role === 'admin' ? await statusPanel() : null;
    return h('div', null,
      h('section', null, h('h2', null, 'Ayarlar'),
        h('div', { class: 'panel stack' }, h('h3', null, 'Görünüm'),
          h('div', { class: 'stack' }, h('span', { class: 'muted small' }, 'Tema'), themeBox),
          h('div', { class: 'stack' }, h('span', { class: 'muted small' }, 'Renk paleti'), palBox),
          h('div', { class: 'stack' }, h('span', { class: 'muted small' }, 'Vurgu rengi (paletin rengini değiştirir)'), accentBox),
          h('div', { class: 'stack' }, h('span', { class: 'muted small' }, 'Sayfa düzeni (Otomatik: geniş ekranlarda yan boşluklar kullanılır ve içerik çok sütuna yayılır)'), layBox),
          h('p', { class: 'muted small' }, 'Bu tercihler yalnızca bu tarayıcıda saklanır. "Sistem" işletim sisteminizin açık/koyu ayarını izler.'))),
      ME.account ? pwPanel() : null,
      ME.role === 'admin' ? backupPanel() : null,
      statusSec,
      h('div', { class: 'grid' },
        h('section', null, h('h3', null, 'Sistem'), h('div', { class: 'panel facts' },
          fact('Sürüm', info.version || '—'),
          fact('Mod', info.mode === 'saas' ? 'SaaS' : 'On-premise'),
          fact('Veri saklama süresi', info.retention_days + ' gün'),
          fact('Probe\'lar için özel ağ engeli', info.block_private ? 'Açık' : 'Kapalı'),
          fact('Tarayıcı kaydı ve senaryoları', info.browser_enabled ? 'Açık' : 'Kapalı'),
          fact('Oturum', info.user + ' (' + (ROLE_LABEL[info.role] || info.role) + ')'))),
        h('section', null, h('h3', null, 'Gizli değer kasası (Vault)'), h('div', { class: 'panel' },
          h('p', null, pill('unknown', 'Yapılandırılmadı')),
          h('p', { class: 'muted' }, 'Henüz bir kasa bağlı değil. Şu an parolalar ve token\'lar (probe/senaryo başlıkları, giriş bilgileri) veritabanında düz metin saklanıyor. Kasa entegrasyonu eklenene kadar yetkisi sınırlı test kullanıcıları kullanın.'))),
        h('section', null, h('h3', null, 'Diğer ayarlar'), h('div', { class: 'panel' },
          h('p', { class: 'muted' }, 'Lisans, asset limiti ve API anahtarları "Lisans ve anahtarlar" sayfasında; sunucu davranışı (mod, saklama süresi, tarayıcı) ortam değişkenleriyle (.env) ayarlanır.')))));
  }

  // Denetim günlüğü: başlık satırında süzgeçler (sunucuda süzülür; durum yenilemede korunur).
  const AUD = { actor: '', action: '', q: '', from: '', to: '', page: 0 };
  async function viewDenetim() {
    await refreshHeader();
    const facets = await api('/api/audit/facets').catch(() => ({ actors: [], actions: [] }));
    const tbody = h('tbody'); const count = h('span', { class: 'muted small fcount', role: 'status' }); const count2 = h('span', { class: 'muted small fcount', role: 'status' });
    const day = (v, end) => { if (!v) return ''; const d = new Date(v + 'T00:00:00'); if (end) d.setDate(d.getDate() + 1); return d.toISOString(); };
    let seq = 0, timer = null;
    const load = async () => {
      const my = ++seq;
      const qs = new URLSearchParams();
      for (const k of ['actor', 'action', 'q']) if (AUD[k]) qs.set(k, AUD[k]);
      qs.set('limit', String(PAGE + 1)); qs.set('offset', String(AUD.page * PAGE));
      if (AUD.from) qs.set('from', day(AUD.from, false));
      if (AUD.to) qs.set('to', day(AUD.to, true));
      try {
        let list = await api('/api/audit?' + qs.toString());
        if (my !== seq) return;
        const more = list.length > PAGE; list = list.slice(0, PAGE);
        count.textContent = list.length ? `${AUD.page * PAGE + 1}–${AUD.page * PAGE + list.length}. kayıt · sayfa ${AUD.page + 1}` : '';
        count2.textContent = count.textContent;
        prevB.disabled = prevT.disabled = AUD.page === 0; nextB.disabled = nextT.disabled = !more;
        tbody.replaceChildren(...(list.length ? list.map((a) => h('tr', null,
          h('td', { class: 'mono small' }, new Date(a.at).toLocaleString((window.LOCALE || 'tr-TR'))), h('td', null, a.actor),
          h('td', { class: 'mono small' }, a.action), h('td', null, a.detail)))
          : [h('tr', null, h('td', { class: 'empty', colspan: 4 }, 'Süzgece uyan kayıt yok.'))]));
      } catch (e) { if (my === seq) count.textContent = count2.textContent = e.message; }
    };
    const later = () => { clearTimeout(timer); timer = setTimeout(load, 250); };
    const sel = (key, label, vals) => { const el = h('select', { 'aria-label': label }, h('option', { value: '' }, 'Tümü'), vals.map((v) => h('option', { value: v }, v)));
      if (AUD[key] && !vals.includes(AUD[key])) el.append(h('option', { value: AUD[key] }, AUD[key])); el.value = AUD[key]; el.addEventListener('change', () => { AUD[key] = el.value; AUD.page = 0; load(); }); return el; };
    const dt = (key, label) => { const el = h('input', { type: 'date', 'aria-label': label, value: AUD[key] }); el.addEventListener('change', () => { AUD[key] = el.value; AUD.page = 0; load(); }); return el; };
    const txt = h('input', { type: 'search', placeholder: 'Ayrıntıda ara…', 'aria-label': 'Ayrıntıda ara', value: AUD.q });
    txt.addEventListener('input', () => { AUD.q = txt.value; AUD.page = 0; later(); });
    const prevB = h('button', { type: 'button', class: 'small', onclick: () => { AUD.page = Math.max(0, AUD.page - 1); load(); } }, '‹ Önceki');
    const nextB = h('button', { type: 'button', class: 'small', onclick: () => { AUD.page++; load(); } }, 'Sonraki ›');
    const prevT = h('button', { type: 'button', class: 'small', onclick: () => { AUD.page = Math.max(0, AUD.page - 1); load(); } }, '‹ Önceki');
    const nextT = h('button', { type: 'button', class: 'small', onclick: () => { AUD.page++; load(); } }, 'Sonraki ›');
    const clear = h('button', { type: 'button', class: 'small', onclick: () => { Object.assign(AUD, { actor: '', action: '', q: '', from: '', to: '', page: 0 }); render(); } }, 'Süzgeçleri temizle');
    load();
    return h('section', null, h('h2', null, 'Denetim günlüğü'),
      h('div', { class: 'row pager' }, count2, h('span', { class: 'row' }, prevT, nextT)),
      h('div', { class: 'tablewrap' }, h('table', { class: 'aud-tbl' },
        h('thead', null,
          h('tr', null, ['Zaman', 'Kullanıcı', 'İşlem', 'Ayrıntı'].map((t) => h('th', null, t))),
          h('tr', { class: 'aud-filters' },
            h('th', null, h('div', { class: 'aud-range' }, dt('from', 'Başlangıç tarihi'), dt('to', 'Bitiş tarihi'))),
            h('th', null, sel('actor', 'Kullanıcıya göre', facets.actors || [])),
            h('th', null, sel('action', 'İşleme göre', facets.actions || [])),
            h('th', null, txt))),
        tbody)),
      h('div', { class: 'row pager' }, count, h('span', { class: 'row' }, prevB, nextB, clear)));
  }

  // ---- Yapılandırma yedeği ----
  function backupPanel() {
    const msg = h('p', { class: 'msg', role: 'status' });
    const dl = h('button', { type: 'button' }, 'Yapılandırmayı indir (JSON)');
    dl.addEventListener('click', async () => {
      try {
        const res = await fetch('/api/export', { headers: { 'X-API-Key': key } });
        if (!res.ok) throw new Error('HTTP ' + res.status);
        const a = h('a', { href: URL.createObjectURL(await res.blob()), download: 'appmon-yapilandirma-' + new Date().toISOString().slice(0, 10) + '.json' });
        document.body.append(a); a.click(); a.remove();
        msg.className = 'msg ok'; msg.textContent = 'İndirildi. Parolalar ve SNMP toplulukları dosyada yoktur.';
      } catch (e) { msg.className = 'msg error'; msg.textContent = e.message; }
    });
    const file = h('input', { type: 'file', accept: 'application/json,.json' });
    file.addEventListener('change', async () => {
      const f = file.files[0]; if (!f) return;
      if (!confirm('"' + f.name + '" içe aktarılsın mı? Yalnızca eksik asset, probe, kural ve bakım pencereleri eklenir; var olanlar değişmez.')) { file.value = ''; return; }
      try {
        const body = JSON.parse(await f.text());
        const r = await api('/api/import', { method: 'POST', body });
        msg.className = 'msg ' + (r.skipped.length ? 'error' : 'ok');
        msg.textContent = r.assets + ' asset, ' + r.probes + ' probe, ' + r.rules + ' kural, ' + r.maintenance + ' bakım penceresi eklendi.' + (r.disabled_for_secrets ? ' ' + r.disabled_for_secrets + ' probe gizli değerleri eksik olduğu için KAPALI eklendi (Probe\'lar sayfasından parola/topluluk girip açın).' : '') + (r.skipped.length ? ' Atlananlar: ' + r.skipped.join(' | ') : '');
      } catch (e) { msg.className = 'msg error'; msg.textContent = e.message; }
      file.value = '';
    });
    return h('section', null, h('div', { class: 'panel stack' }, h('h3', null, 'Yapılandırma yedeği'),
      h('p', { class: 'muted small' }, 'Asset, probe, alarm kuralı ve bakım pencerelerini JSON olarak indirir veya başka bir sunucuya yükler. Parolalar, SNMP toplulukları, HTTP başlıkları, bildirim/SIEM kanalları ve senaryolar dahil değildir; metrik geçmişi ve her şey için veritabanı yedeği alın (pg_dump).'),
      h('div', { class: 'row' }, dl, h('label', { class: 'stack' }, h('span', { class: 'small muted' }, 'İçe aktar'), file)), msg));
  }
  // ---- Herkese açık durum sayfası ----
  async function statusPanel() {
    let d = { assets: [] };
    try { d = await api('/api/status-page'); } catch (e) { return null; }
    const msg = h('p', { class: 'msg', role: 'status' });
    const rows = d.assets.map((a) => {
      const cb = h('input', { type: 'checkbox' }); cb.checked = a.public;
      const nm = h('input', { value: a.status_name || '', placeholder: a.name, maxlength: '100' });
      const save = async () => {
        try { await api('/api/assets/' + a.id + '/status-page', { method: 'PUT', body: { public: cb.checked, name: nm.value.trim() } }); msg.className = 'msg ok'; msg.textContent = 'Kaydedildi.'; }
        catch (e) { msg.className = 'msg error'; msg.textContent = e.message; }
      };
      cb.addEventListener('change', save); nm.addEventListener('change', save);
      return h('tr', null, h('td', null, cb), h('td', null, a.name), h('td', null, nm));
    });
    const link = h('a', { href: '/status', target: '_blank', rel: 'noopener' }, location.origin + '/status');
    return h('section', null, h('div', { class: 'panel stack' }, h('h3', null, 'Herkese açık durum sayfası'),
      h('p', { class: 'muted small' }, 'İşaretlediğiniz asset\'lerin anlık durumu ve son 90 günlük erişilebilirliği, giriş gerektirmeyen bir sayfada yayınlanır: ', link, '. Yalnızca burada işaretli asset\'ler ve görünen adları görünür (ayrıntı, hata iletisi veya adres gösterilmez). Başlık için sunucuda APM_STATUS_TITLE tanımlayın. Sayfayı internete açmadan önce hangi adların görüneceğini kontrol edin.'),
      msg, table(['Yayınla', 'Asset', 'Görünen ad (boşsa asset adı)'], rows, 'Asset yok.')));
  }
  // ---- Kullanıcı hesapları ----
  function pwPanel() {
    const msg = h('p', { class: 'msg', role: 'status' });
    const o = h('input', { type: 'password', autocomplete: 'current-password' }), n = h('input', { type: 'password', autocomplete: 'new-password', placeholder: 'en az 10 karakter' });
    const f = h('form', { class: 'inline' }, field('Mevcut parola', o), field('Yeni parola', n), h('button', { class: 'btn', type: 'submit' }, 'Parolayı değiştir'));
    f.addEventListener('submit', async (e) => {
      e.preventDefault();
      try { await api('/api/me/password', { method: 'POST', body: { old: o.value, new: n.value } }); o.value = n.value = ''; msg.className = 'msg ok'; msg.textContent = 'Parola değiştirildi.'; }
      catch (x) { msg.className = 'msg error'; msg.textContent = x.message; }
    });
    return h('section', null, h('div', { class: 'panel stack' }, h('h3', null, 'Hesabım'), f, msg));
  }
  const USER_ROLES = [['viewer', 'İzleyici'], ['operator', 'İşletmeci'], ['admin', 'Yönetici']];
  // ---- Kiracılar (yalnızca platform yöneticisi) ----
  function tenantForm(t, done) {
    const q = t ? t.quotas : { asset_limit: 50, probe_limit: 0, user_limit: 25, min_interval_sec: 10, retention_days: 0, allow_browser: false, allow_crawl: false, allow_ssh: false };
    const num = (v, min) => h('input', { type: 'number', min: String(min || 0), value: String(v), required: true });
    const name = h('input', { required: true, maxlength: '60', value: t ? t.name : '' });
    const slug = h('input', { maxlength: '40', placeholder: 'boşsa addan üretilir', value: t ? t.slug : '', pattern: '[a-z0-9\\-]*' });
    const au = h('input', { required: true, maxlength: '64', autocomplete: 'off' });
    const ap = h('input', { type: 'password', required: true, autocomplete: 'new-password', placeholder: 'en az 10 karakter' });
    const al = num(q.asset_limit), pl = num(q.probe_limit), ul = num(q.user_limit, 1), mi = num(q.min_interval_sec, 5), rd = num(q.retention_days);
    const chk = (v) => h('input', { type: 'checkbox', checked: v ? true : undefined });
    const ab = chk(q.allow_browser), ac = chk(q.allow_crawl), as = chk(q.allow_ssh);
    const lab = (txt, el) => h('label', { class: 'check' }, el, ' ' + txt);
    const msg = h('p', { class: 'msg small' });
    let close;
    const f = h('form', { class: 'stack', onsubmit: async (e) => {
      e.preventDefault(); msg.textContent = '';
      const lim = t && t.licensed ? {} : { asset_limit: Number(al.value), probe_limit: Number(pl.value), user_limit: Number(ul.value), min_interval_sec: Number(mi.value), retention_days: Number(rd.value), allow_browser: ab.checked, allow_crawl: ac.checked, allow_ssh: as.checked };
      try {
        if (t) await api('/api/tenants/' + t.id, { method: 'PUT', body: { name: name.value.trim(), slug: slug.value.trim() || undefined, ...lim } });
        else await api('/api/tenants', { method: 'POST', body: { name: name.value.trim(), slug: slug.value.trim() || undefined, admin_username: au.value.trim(), admin_password: ap.value, ...lim } });
        close(); done();
      } catch (err) { msg.className = 'msg error small'; msg.textContent = err.message; }
    } },
    field('Kiracı (kurum) adı', name), field('Durum sayfası adresi (/status/…)', slug),
    t ? null : field('Yönetici kullanıcı adı', au), t ? null : field('Yönetici parolası', ap),
    t && t.licensed ? h('p', { class: 'muted small' }, `Kotalar imzalı lisans dosyasıyla yönetiliyor (müşteri: ${t.license_customer}). Değiştirmek için "Lisans üret" ile yeni bir .lic üretin.`) : [
      h('div', { class: 'grid' }, field('Asset limiti', al), field('Probe limiti (0 = asset limiti kadar)', pl), field('Kullanıcı limiti', ul), field('En kısa tarama aralığı (sn)', mi), field('Saklama süresi (gün, 0 = sunucu varsayılanı)', rd)),
      h('div', { class: 'stack' }, lab('Tarayıcı senaryolarına izin ver', ab), lab('Keşfet (site tarama) özelliğine izin ver', ac), lab('SSH ile sunucu izlemeye izin ver', as))],
    msg, h('div', { class: 'row' }, h('span'), h('button', { type: 'submit', class: 'primary' }, t ? 'Kaydet' : 'Kiracı oluştur')));
    close = openModal(t ? 'Kiracıyı düzenle: ' + t.name : 'Yeni kiracı', f, false);
    name.focus();
  }
  // Kiracı için imzalı lisans dosyası (.lic) üretir ve indirir; dosya kiracı yöneticisine iletilir.
  function tenantLicense(t) {
    const q = t.quotas;
    const num = (v, min) => h('input', { type: 'number', min: String(min || 0), value: String(v), required: true });
    const cu = h('input', { maxlength: '100', value: t.license_customer || t.name, required: true });
    const al = num(q.asset_limit || 50, 1), pl = num(q.probe_limit), ul = num(q.user_limit, 0), mi = num(q.min_interval_sec, 5), rd = num(q.retention_days);
    const vu = h('input', { type: 'date', value: q.valid_until ? q.valid_until.slice(0, 10) : '' });
    const chk = (v) => h('input', { type: 'checkbox', checked: v ? true : undefined });
    const ab = chk(q.allow_browser), ac = chk(q.allow_crawl), as = chk(q.allow_ssh);
    const lab = (txt, el) => h('label', { class: 'check' }, el, ' ' + txt);
    const msg = h('p', { class: 'msg small' });
    const f = h('form', { class: 'stack', onsubmit: async (e) => {
      e.preventDefault(); msg.className = 'msg small'; msg.textContent = '';
      const body = { customer: cu.value.trim(), asset_limit: Number(al.value), probe_limit: Number(pl.value), user_limit: Number(ul.value), min_interval_sec: Number(mi.value), retention_days: Number(rd.value),
        allow_browser: ab.checked, allow_crawl: ac.checked, allow_ssh: as.checked, valid_until: vu.value ? new Date(vu.value + 'T23:59:59Z').toISOString() : undefined };
      try {
        const r = await fetch('/api/tenants/' + t.id + '/license-file', { method: 'POST', headers: { 'X-API-Key': key, 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
        if (!r.ok) { let d = {}; try { d = await r.json(); } catch (x) { /* yoksay */ } throw new Error(d.error || ('HTTP ' + r.status)); }
        const blob = await r.blob();
        const a = h('a', { href: URL.createObjectURL(blob), download: 'appmon-' + t.slug + '-' + new Date().toISOString().slice(0, 10).replace(/-/g, '') + '.lic' });
        document.body.append(a); a.click(); a.remove();
        msg.className = 'msg ok small'; msg.textContent = 'Lisans dosyası indirildi. Kiracı yöneticisine iletin; Lisans sayfasından yüklenir.';
        setTimeout(() => URL.revokeObjectURL(a.href), 5000);
      } catch (err) { msg.className = 'msg error small'; msg.textContent = err.message; }
    } },
    h('p', { class: 'muted small' }, `"${t.name}" kiracısı için imzalı lisans üretilir. Dosya yalnızca bu kiracıya (/${t.slug}) yüklenebilir; yüklenince kotalar dosyadan okunur ve elle değiştirilemez.`),
    field('Müşteri adı', cu),
    h('div', { class: 'grid' }, field('Asset limiti', al), field('Probe limiti (0 = asset limiti kadar)', pl), field('Kullanıcı limiti (0 = sınırsız)', ul), field('En kısa tarama aralığı (sn)', mi), field('Saklama süresi (gün, 0 = varsayılan)', rd), field('Geçerlilik tarihi (boşsa süresiz)', vu)),
    h('div', { class: 'stack' }, lab('Tarayıcı senaryoları', ab), lab('Keşfet (site tarama)', ac), lab('SSH ile sunucu izleme', as)),
    msg, h('div', { class: 'row' }, h('span'), h('button', { type: 'submit', class: 'primary' }, '.lic dosyası üret ve indir')));
    openModal('Lisans üret: ' + t.name, f, false);
    cu.focus();
  }
  function tenantDelete(t, done) {
    const useKey = !ME.account;
    const cf = h('input', { required: true, placeholder: t.name, autocomplete: 'off' });
    const inp = h('input', { type: 'password', autocomplete: useKey ? 'off' : 'current-password', placeholder: useKey ? 'API anahtarınızı yeniden girin' : 'Parolanız', required: true });
    const msg = h('p', { class: 'msg small' });
    let close;
    const f = h('form', { class: 'stack', onsubmit: async (e) => {
      e.preventDefault(); msg.textContent = '';
      try { await api('/api/tenants/' + t.id, { method: 'DELETE', body: { confirm: cf.value, password: useKey ? undefined : inp.value, api_key: useKey ? inp.value : undefined } }); close(); done(); }
      catch (err) { msg.className = 'msg error small'; msg.textContent = err.message; }
    } }, h('p', { class: 'small' }, `"${t.name}" kiracısının TÜM verisi (asset, probe, senaryo, kullanıcı, kasa, denetim kayıtları) kalıcı olarak silinir ve geri alınamaz. Onaylamak için kiracı adını yazın ve ${useKey ? 'API anahtarınızı' : 'parolanızı'} girin.`),
    cf, inp, msg, h('div', { class: 'row' }, h('span'), h('button', { type: 'submit', class: 'danger' }, 'Kalıcı olarak sil')));
    close = openModal('Kiracı silinsin mi?', f, true);
    cf.focus();
  }
  function tenantAdmin(t) {
    const un = h('input', { required: true, maxlength: '64', autocomplete: 'off' }), pw = h('input', { type: 'password', required: true, autocomplete: 'new-password', placeholder: 'en az 10 karakter' });
    const msg = h('p', { class: 'msg small' });
    let close;
    const f = h('form', { class: 'stack', onsubmit: async (e) => {
      e.preventDefault();
      try { await api('/api/tenants/' + t.id + '/admins', { method: 'POST', body: { username: un.value.trim(), password: pw.value } }); msg.className = 'msg ok small'; msg.textContent = 'Yönetici eklendi.'; pw.value = ''; }
      catch (err) { msg.className = 'msg error small'; msg.textContent = err.message; }
    } }, h('p', { class: 'muted small' }, 'Kiracının yöneticisi parolasını unuttuysa veya hiç yönetici kalmadıysa buradan yeni yönetici ekleyin. Kullanıcı adları tüm kiracılarda benzersizdir.'),
    field('Kullanıcı adı', un), field('Parola', pw), msg, h('div', { class: 'row' }, h('span'), h('button', { type: 'submit', class: 'primary' }, 'Yönetici ekle')));
    close = openModal('Yönetici ekle: ' + t.name, f, true);
    un.focus();
  }
  async function viewKiracilar() {
    await refreshHeader();
    const d = await api('/api/tenants');
    const msg = h('p', { class: 'msg', role: 'status' });
    const use = (u, lim) => lim ? `${u} / ${lim}` : String(u);
    const rows = d.tenants.map((t) => {
      const q = t.quotas, u = t.usage;
      const btn = (label, fn, cls) => h('button', { type: 'button', class: 'btn small ' + (cls || ''), onclick: fn }, label);
      const acts = [btn('Düzenle', () => tenantForm(t, render)), btn('Lisans üret', () => tenantLicense(t)), btn('Yönetici ekle', () => tenantAdmin(t))];
      if (!t.root) {
        acts.push(btn(t.disabled ? 'Etkinleştir' : 'Durdur', async () => {
          if (!t.disabled && !confirm(`"${t.name}" durdurulsun mu? Tüm oturumları kapanır, izleme durur; veriler silinmez.`)) return;
          try { await api('/api/tenants/' + t.id, { method: 'PUT', body: { disabled: !t.disabled } }); render(); } catch (x) { msg.className = 'msg error'; msg.textContent = x.message; }
        }));
        acts.push(btn('Sil', () => tenantDelete(t, render), 'danger'));
      }
      return h('tr', null,
        h('td', null, h('b', null, t.name), t.root ? h('span', { class: 'muted small' }, ' (ana kiracı)') : null, t.licensed ? h('span', { class: 'muted small' }, ' · lisanslı: ' + t.license_customer) : null, h('div', { class: 'muted small mono' }, '/status/' + t.slug)),
        h('td', null, t.disabled ? pill('down', 'Durduruldu') : pill('up', 'Etkin')),
        h('td', { class: 'mono' }, use(u.assets, q.asset_limit)), h('td', { class: 'mono' }, use(u.probes, q.probe_limit || q.asset_limit)), h('td', { class: 'mono' }, use(u.users, q.user_limit)),
        h('td', { class: 'small' }, [q.allow_browser ? 'tarayıcı' : null, q.allow_crawl ? 'keşfet' : null, q.allow_ssh ? 'ssh' : null].filter(Boolean).join(', ') || '—'),
        h('td', { class: 'mono small' }, t.last_activity ? ago(t.last_activity) : '—'),
        h('td', { class: 'row' }, acts));
    });
    return h('div', { class: 'stack' }, h('section', null, h('h2', null, 'Kiracılar'),
      h('p', { class: 'muted' }, `Her kiracı (kurum) kendi asset, probe, kullanıcı, kasa ve denetim verisini görür; başka kiracının verisine erişemez. En fazla ${d.max} kiracı açılabilir (APM_MAX_TENANTS). Lisans, kota ve sürüm güncelleme yalnızca bu sayfadan (ana kiracı yöneticisi) yönetilir.`),
      h('div', { class: 'row' }, h('button', { type: 'button', class: 'primary', disabled: d.tenants.length >= d.max ? true : undefined, onclick: () => tenantForm(null, render) }, '+ Yeni kiracı'), h('span', { class: 'muted small' }, `${d.tenants.length} / ${d.max}`)),
      msg, table(['Kiracı', 'Durum', 'Asset', 'Probe', 'Kullanıcı', 'Özellikler', 'Son işlem', ''], rows, 'Kiracı yok.')));
  }

  async function viewKullanicilar() {
    await refreshHeader();
    const d = await api('/api/users');
    const msg = h('p', { class: 'msg', role: 'status' });
    const fail = (x) => { msg.className = 'msg error'; msg.textContent = x.message; };
    const un = h('input', { maxlength: '64', required: true, autocomplete: 'off' }), pw = h('input', { type: 'password', autocomplete: 'new-password', placeholder: 'en az 10 karakter', required: true });
    const rl = select(null, USER_ROLES, 'viewer');
    const add = h('form', { class: 'inline' }, field('Kullanıcı adı', un), field('Parola', pw), field('Rol', rl), h('button', { class: 'btn', type: 'submit' }, 'Kullanıcı ekle'));
    add.addEventListener('submit', async (e) => {
      e.preventDefault();
      try { await api('/api/users', { method: 'POST', body: { username: un.value.trim(), password: pw.value, role: rl.value } }); render(); } catch (x) { fail(x); }
    });
    const rows = d.users.map((u) => {
      const role = select(null, USER_ROLES, u.role);
      role.addEventListener('change', async () => { try { await api('/api/users/' + u.id, { method: 'PUT', body: { role: role.value } }); msg.className = 'msg ok'; msg.textContent = u.username + ' rolü güncellendi; açık oturumları kapatıldı.'; } catch (x) { fail(x); } });
      const act = (label, body, confirmText) => {
        const b = h('button', { type: 'button', class: 'btn small' }, label);
        b.addEventListener('click', async () => {
          if (confirmText && !confirm(confirmText)) return;
          try { await api('/api/users/' + u.id, { method: 'PUT', body }); render(); } catch (x) { fail(x); }
        });
        return b;
      };
      const setPw = h('button', { type: 'button', class: 'btn small' }, 'Parola belirle');
      setPw.addEventListener('click', async () => {
        const v = prompt(u.username + ' için yeni parola (en az 10 karakter):');
        if (!v) return;
        try { await api('/api/users/' + u.id, { method: 'PUT', body: { password: v } }); msg.className = 'msg ok'; msg.textContent = 'Parola belirlendi.'; } catch (x) { fail(x); }
      });
      const del = h('button', { type: 'button', class: 'btn small danger' }, 'Sil');
      del.addEventListener('click', async () => { if (!confirm(u.username + ' silinsin mi?')) return; try { await api('/api/users/' + u.id, { method: 'DELETE' }); render(); } catch (x) { fail(x); } });
      return h('tr', null, h('td', null, u.username), h('td', null, u.source === 'ldap' ? 'LDAP' : 'Yerel'), h('td', null, role),
        h('td', null, u.locked ? pill('down', 'Kilitli') : pill(u.enabled ? 'up' : 'unknown', u.enabled ? 'Etkin' : 'Devre dışı')),
        h('td', { class: 'mono small' }, u.last_login ? new Date(u.last_login).toLocaleString((window.LOCALE || 'tr-TR')) : '—'),
        h('td', null, h('div', { class: 'row' }, act(u.enabled ? 'Devre dışı bırak' : 'Etkinleştir', { enabled: !u.enabled }), u.locked ? act('Kilidi aç', { unlock: true }) : null, u.source === 'local' ? setPw : null, del)));
    });
    return h('section', null, h('h2', null, 'Kullanıcılar'),
      h('p', { class: 'muted small' }, 'Kullanıcılar kullanıcı adı ve parolayla giriş yapar (12 saatlik oturum; 5 hatalı denemede hesap 5 dakika kilitlenir). API anahtarları ayrıca çalışmaya devam eder; ajanlar yalnızca anahtar kullanır. Rol veya parola değişince kullanıcının açık oturumları kapanır.'),
      h('p', { class: 'muted small' }, d.ldap ? 'LDAP girişi açık: dizin kullanıcıları ilk girişte "' + (USER_ROLES.find((r) => r[0] === d.ldap_default_role) || [0, d.ldap_default_role])[1] + '" rolüyle otomatik eklenir; rolü buradan değiştirebilirsiniz.' : 'LDAP girişi kapalı (APM_LDAP_URL ve APM_LDAP_DN_TEMPLATE ile açılır).'),
      add, msg,
      table(['Kullanıcı', 'Kaynak', 'Rol', 'Durum', 'Son giriş', ''], rows, 'Henüz kullanıcı yok; yalnızca API anahtarıyla giriş yapılabilir.'));
  }

  // ---- SLA raporu ----
  const fmtDur = (sec) => { const m = Math.round(sec / 60); return m >= 60 ? Math.floor(m / 60) + ' sa ' + (m % 60) + ' dk' : m + ' dk'; };
  const slaRange = { month: '', period: 'month', win: 'all', hs: 8, he: 17, days: [1, 2, 3, 4, 5], group: 'none' };
  const SLA_PERIODS = [['month', 'Seçilen ay'], ['today', 'Bugün'], ['last24h', 'Son 24 saat'], ['week', 'Bu hafta (Pazartesi\'den)'], ['last7', 'Son 7 gün'], ['last30', 'Son 30 gün']];
  const SLA_WINS = [['all', '7/24 (tüm zaman)'], ['biz', 'Mesai saatleri 08:00–17:00 (Pzt–Cum)'], ['ext', 'Geniş mesai 07:00–24:00 (her gün)'], ['custom', 'Özel saat ve gün…']];
  const SLA_GROUPS = [['none', 'Asset başına özet'], ['hour', 'Saatlik'], ['day', 'Günlük'], ['week', 'Haftalık']];
  const SLA_DAYS = [[1, 'Pzt'], [2, 'Sal'], [3, 'Çar'], [4, 'Per'], [5, 'Cum'], [6, 'Cmt'], [0, 'Paz']];

  // Seçili döneme ve zaman filtresine göre /api/sla sorgu dizesi üretir.
  function slaQuery() {
    const pad = (n) => String(n).padStart(2, '0'), now = new Date();
    const ymd = (d) => d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate());
    const ymdhm = (d) => ymd(d) + 'T' + pad(d.getHours()) + ':' + pad(d.getMinutes());
    const back = (n) => new Date(now.getFullYear(), now.getMonth(), now.getDate() - n);
    let from, to;
    switch (slaRange.period) {
      case 'today': from = to = ymd(now); break;
      case 'last24h': from = ymdhm(new Date(now.getTime() - 24 * 3600 * 1000)); to = ymdhm(now); break;
      case 'week': from = ymd(back((now.getDay() + 6) % 7)); to = ymd(now); break;
      case 'last7': from = ymd(back(6)); to = ymd(now); break;
      case 'last30': from = ymd(back(29)); to = ymd(now); break;
      default: {
        const [yy, mm] = slaRange.month.split('-').map(Number);
        from = slaRange.month + '-01'; to = slaRange.month + '-' + pad(new Date(yy, mm, 0).getDate());
      }
    }
    let q = '?from=' + encodeURIComponent(from) + '&to=' + encodeURIComponent(to);
    const w = slaRange.win;
    if (w === 'biz') q += '&hours=8-17&days=1,2,3,4,5';
    else if (w === 'ext') q += '&hours=7-24';
    else if (w === 'custom') q += '&hours=' + slaRange.hs + '-' + slaRange.he + (slaRange.days.length && slaRange.days.length < 7 ? '&days=' + slaRange.days.join(',') : '');
    if (slaRange.group !== 'none') q += '&group=' + slaRange.group;
    return q;
  }

  // SLA sıfırlama: yönetici parolası (API anahtarıyla girenlerde anahtar) yeniden istenir.
  function slaResetDialog(ids, name) {
    const useKey = !ME.account;
    const inp = h('input', { type: 'password', required: true, autocomplete: useKey ? 'off' : 'current-password', placeholder: useKey ? 'API anahtarınızı yeniden girin' : 'Yönetici parolanız' });
    const msg = h('p', { class: 'msg small' });
    let close;
    const f = h('form', { class: 'stack', onsubmit: async (e) => {
      e.preventDefault(); msg.textContent = '';
      try { await api('/api/sla/reset', { method: 'POST', body: { asset_ids: ids || undefined, all: ids ? undefined : true, password: useKey ? undefined : inp.value, api_key: useKey ? inp.value : undefined } }); close(); render(); }
      catch (err) { msg.className = 'msg error small'; msg.textContent = err.message; }
    } },
    h('p', { class: 'small' }, ids ? 'Bu asset\'in SLA özeti (erişilebilirlik, kesinti ve olay sayıları) silinir ve hesaplama şu andan itibaren yeniden başlar.' : 'Tüm asset\'lerin SLA özeti silinir ve hesaplama şu andan itibaren yeniden başlar.'),
    h('p', { class: 'muted small' }, 'Ham ölçümler, alarmlar ve denetim kaydı silinmez; sıfırlama denetim kaydına yazılır. Bu işlem geri alınamaz. Durum sayfasındaki 90 günlük erişilebilirlik de sıfırlanır.'),
    h('p', { class: 'muted small' }, `Onaylamak için ${useKey ? 'API anahtarınızı' : 'parolanızı'} girin.`),
    inp, msg, h('div', { class: 'row' }, h('span'), h('button', { type: 'submit', class: 'danger' }, 'SLA\'yı sıfırla')));
    close = openModal(ids ? 'SLA sıfırlansın mı? · ' + name : 'Tüm SLA\'lar sıfırlansın mı?', f, true);
    inp.focus();
  }

  async function viewSla() {
    await refreshHeader();
    const now = new Date(), pad = (n) => String(n).padStart(2, '0');
    if (!slaRange.month) slaRange.month = now.getFullYear() + '-' + pad(now.getMonth() + 1);
    const q = slaQuery();
    const rep = await api('/api/sla' + q);
    const msg = h('p', { class: 'msg', role: 'status' });
    const rerender = () => render();
    const per = select(null, SLA_PERIODS, slaRange.period);
    per.addEventListener('change', () => { slaRange.period = per.value; rerender(); });
    const mon = h('input', { type: 'month', value: slaRange.month });
    mon.addEventListener('change', () => { if (mon.value) { slaRange.month = mon.value; rerender(); } });
    const win = select(null, SLA_WINS, slaRange.win);
    win.addEventListener('change', () => { slaRange.win = win.value; rerender(); });
    const grp = select(null, SLA_GROUPS, slaRange.group);
    grp.addEventListener('change', () => { slaRange.group = grp.value; rerender(); });
    let custom = null;
    if (slaRange.win === 'custom') {
      const hs = h('input', { type: 'number', min: '0', max: '23', value: String(slaRange.hs), class: 'sla-hour' });
      const he = h('input', { type: 'number', min: '1', max: '24', value: String(slaRange.he), class: 'sla-hour' });
      const apply = () => { const a = Number(hs.value), b = Number(he.value); if (Number.isInteger(a) && Number.isInteger(b) && a >= 0 && a <= 23 && b >= 0 && b <= 24 && a !== b) { slaRange.hs = a; slaRange.he = b; rerender(); } else { msg.className = 'msg error'; msg.textContent = 'Başlangıç 0-23, bitiş 0-24 olmalı ve ikisi aynı olmamalı (ör. 8-17, 7-24, 22-6).'; } };
      hs.addEventListener('change', apply); he.addEventListener('change', apply);
      const days = h('div', { class: 'sla-days' }, SLA_DAYS.map(([d, t]) => {
        const cb = h('input', { type: 'checkbox' }); cb.checked = slaRange.days.includes(d);
        cb.addEventListener('change', () => { slaRange.days = SLA_DAYS.map(([x]) => x).filter((x) => (x === d ? cb.checked : slaRange.days.includes(x))); rerender(); });
        return h('label', { class: 'check' }, cb, ' ' + t);
      }));
      custom = h('div', { class: 'sla-custom' }, field('Başlangıç saati', hs), field('Bitiş saati', he), field('Günler', days));
    }
    const csv = h('button', { class: 'btn', type: 'button' }, 'CSV indir');
    csv.addEventListener('click', async () => {
      try {
        const res = await fetch('/api/sla.csv' + q, { headers: { 'X-API-Key': key } });
        if (!res.ok) throw new Error('HTTP ' + res.status);
        const a = h('a', { href: URL.createObjectURL(await res.blob()), download: 'sla-' + (rep.from || '').slice(0, 10) + '.csv' });
        document.body.append(a); a.click(); a.remove();
      } catch (e) { msg.className = 'msg error'; msg.textContent = e.message; }
    });
    const isAdmin = ME.role === 'admin';
    const met = rep.rows.filter((r) => r.met).length;
    const rows = rep.rows.map((r) => {
      const tgt = h('input', { type: 'number', step: '0.001', min: '50', max: '100', value: String(r.target_pct), title: r.custom_target ? 'Özel hedef' : 'Varsayılan hedef' });
      tgt.disabled = !canWrite();
      tgt.addEventListener('change', async () => {
        try { await api('/api/assets/' + r.asset_id + '/sla', { method: 'PUT', body: { target_pct: tgt.value === '' ? null : Number(tgt.value) } }); render(); }
        catch (e) { msg.className = 'msg error'; msg.textContent = e.message; }
      });
      const bar = h('span', { class: 'hopbar' + (r.met ? '' : ' bad') }); bar.style.width = Math.max(2, Math.min(100, r.availability_pct)) + '%';
      const net = Math.max(0, r.down_s - r.maint_s);
      return h('tr', null, h('td', null, r.name, r.group ? h('div', { class: 'muted small' }, r.group) : null),
        h('td', { class: 'mono' }, '%' + r.availability_pct.toFixed(3)),
        h('td', null, h('span', { class: 'hopwrap' }, bar)),
        h('td', null, tgt),
        h('td', null, pill(r.met ? 'up' : 'down', r.met ? 'Karşılandı' : 'Karşılanmadı')),
        h('td', { class: 'mono small' }, fmtDur(net)), h('td', { class: 'mono small' }, r.maint_s ? fmtDur(r.maint_s) : '—'),
        h('td', { class: 'mono small' }, r.degraded_s ? fmtDur(r.degraded_s) : '—'), h('td', { class: 'mono' }, String(r.incidents)),
        isAdmin ? h('td', null, h('button', { type: 'button', class: 'small', onclick: () => slaResetDialog([r.asset_id], r.name) }, 'Sıfırla')) : null);
    });
    const heads = ['Asset', 'Erişilebilirlik', '', 'Hedef %', 'Durum', 'Kesinti', 'Bakımda', 'Kısmi kesinti', 'Olay'].concat(isAdmin ? ['SLA'] : []);
    let seriesBox = null;
    if (rep.series) {
      const srows = rep.series.map((p) => {
        const bar = h('span', { class: 'hopbar' + (p.availability_pct >= rep.default_target ? '' : ' bad') }); bar.style.width = Math.max(2, Math.min(100, p.availability_pct)) + '%';
        return h('tr', null, h('td', { class: 'mono' }, p.label), h('td', { class: 'mono' }, '%' + p.availability_pct.toFixed(3)), h('td', null, h('span', { class: 'hopwrap' }, bar)),
          h('td', { class: 'mono small' }, fmtDur(Math.max(0, p.down_s - p.maint_s))), h('td', { class: 'mono' }, String(p.incidents)), h('td', { class: 'mono small' }, fmtDur(Math.max(0, p.observed_s - p.maint_s))));
      });
      seriesBox = h('div', { class: 'stack' }, h('h3', null, 'Dönem kırılımı: ' + (SLA_GROUPS.find(([k]) => k === slaRange.group) || [])[1]),
        table(['Dönem', 'Erişilebilirlik', '', 'Kesinti', 'Olay', 'Gözlem'], srows, 'Bu filtre için veri yok.', PAGE),
        h('p', { class: 'muted small' }, 'Kırılım tüm asset\'lerin toplamıdır (süre ağırlıklı).'));
    }
    const winNote = slaRange.win === 'all' && slaRange.period === 'month' && slaRange.group === 'none' ? null
      : h('p', { class: 'muted small' }, 'Zaman filtresi saatlik özetten hesaplanır: yalnızca seçili saat ve günlerdeki ölçümler SLA\'ya katılır, filtre dışındaki kesintiler sayılmaz. Saatlik özet bu sürümden itibaren (ve ham ölçüm saklama süresi kadar geriye) tutulur' + (rep.hourly_from ? '; en eski saatlik kayıt: ' + rep.hourly_from.replace('T', ' ') + '.' : '.'));
    return h('section', null, h('h2', null, 'SLA raporu'),
      h('p', { class: 'muted small' }, 'Erişilebilirlik, asset durumu ölçümlerinden süre ağırlıklı hesaplanır (varsayılan hedef %' + rep.default_target + '). Bakım pencerelerindeki kesintiler hesaba katılmaz. Özet veriler ham ölçüm saklama süresinden bağımsız olarak uzun süre tutulur.'),
      h('div', { class: 'inline' }, field('Dönem', per), slaRange.period === 'month' ? field('Ay', mon) : null, field('Zaman filtresi', win), field('Gruplama', grp), csv,
        isAdmin ? h('button', { type: 'button', class: 'danger', onclick: () => slaResetDialog(null) }, 'Tümünü sıfırla') : null),
      custom, winNote,
      msg,
      h('p', { class: 'small' }, rep.rows.length ? met + ' / ' + rep.rows.length + ' asset hedefi karşıladı.' : ''),
      table(heads, rows, 'Bu dönem ve filtre için veri yok.'),
      seriesBox);
  }

  // ---- Ağ araçları: yol izleme ve SNMP trap'leri ----
  async function viewAg() {
    await refreshHeader();
    const msg = h('p', { class: 'msg', role: 'status' });
    const tgt = h('input', { placeholder: '10.0.0.1 veya ornek.com', maxlength: '253', required: true });
    const btn = h('button', { class: 'btn', type: 'submit' }, 'Yolu izle');
    const out = h('div');
    const form = h('form', { class: 'inline' }, field('Hedef', tgt), btn);
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      msg.className = 'msg'; msg.textContent = 'İzleniyor… (en fazla 25 sn)'; btn.disabled = true;
      try {
        const r = await api('/api/trace', { method: 'POST', body: { target: tgt.value.trim() } });
        const max = Math.max(1, ...r.hops.map((x) => x.rtt_ms || 0));
        const rows = r.hops.map((x) => {
          const bar = h('span', { class: 'hopbar' }); bar.style.width = Math.max(2, Math.round(100 * (x.rtt_ms || 0) / max)) + '%';
          return h('tr', null, h('td', { class: 'mono' }, String(x.ttl)),
            h('td', { class: 'mono' }, x.ip || '*'), h('td', { class: 'small' }, x.name || ''),
            h('td', { class: 'mono' }, x.ip ? x.rtt_ms.toFixed(1) + ' ms' : '—'),
            h('td', { class: x.loss_pct > 0 ? 'bad' : '' }, '%' + Math.round(x.loss_pct)),
            h('td', null, h('span', { class: 'hopwrap' }, bar)));
        });
        msg.className = 'msg ' + (r.reached ? 'ok' : 'error');
        msg.textContent = r.reached ? r.hops.length + ' sıçramada ' + r.dest_ip + ' adresine ulaşıldı.' : r.dest_ip + ' adresine ulaşılamadı; yol ' + r.hops.length + ' sıçramada kesildi (güvenlik duvarı ICMP\'yi engelliyor olabilir).';
        out.replaceChildren(table(['#', 'Adres', 'Ad', 'Gecikme', 'Kayıp', ''], rows, 'Sonuç yok.'));
      } catch (err) { msg.className = 'msg error'; msg.textContent = err.message; out.replaceChildren(); }
      btn.disabled = false;
    });
    // Ağ keşfi
    const dmsg = h('p', { class: 'msg', role: 'status' });
    const cidr = h('input', { placeholder: '192.168.1.0/24', maxlength: '20' });
    const dbtn = h('button', { type: 'submit' }, 'Taramayı başlat');
    const dout = h('div');
    const dform = h('form', { class: 'inline' }, field('Ağ aralığı (CIDR, en çok /22)', cidr), dbtn);
    const showHosts = (job) => {
      const sel = new Set();
      const rows = job.hosts.map((x) => {
        const cb = h('input', { type: 'checkbox' });
        cb.addEventListener('change', () => { if (cb.checked) sel.add(x); else sel.delete(x); addBtn.disabled = !sel.size; });
        return h('tr', null, h('td', null, cb), h('td', { class: 'mono' }, x.ip), h('td', { class: 'small' }, x.name || ''),
          h('td', { class: 'mono small' }, x.ping ? x.rtt_ms.toFixed(1) + ' ms' : '—'), h('td', { class: 'mono small' }, x.ports.join(', ') || '—'));
      });
      const addBtn = h('button', { type: 'button' }, 'Seçilenleri izlemeye ekle'); addBtn.disabled = true;
      addBtn.addEventListener('click', async () => {
        addBtn.disabled = true; let ok = 0, bad = 0, last = '';
        for (const x of sel) {
          const name = x.name || x.ip, tests = [];
          if (x.ping) tests.push({ type: 'icmp', target: x.ip });
          for (const pt of x.ports) tests.push({ type: 'tcp', target: x.ip + ':' + pt });
          for (const t of tests) {
            try { await api('/api/probes', { method: 'POST', body: { asset_name: name, type: t.type, target: t.target, interval_sec: 30, timeout_ms: 5000 } }); ok++; }
            catch (e) { bad++; last = e.message; }
          }
        }
        dmsg.className = 'msg ' + (bad ? 'error' : 'ok');
        dmsg.textContent = ok + ' probe eklendi' + (bad ? ', ' + bad + ' hata (' + last + ')' : '') + '. Probe\'lar sayfasından düzenleyebilirsiniz.';
      });
      dout.replaceChildren(table(['', 'Adres', 'Ad', 'Ping', 'Açık portlar'], rows, 'Yanıt veren makine bulunamadı.'), h('div', { class: 'row' }, addBtn));
    };
    const poll = async (id) => {
      try {
        const j = await api('/api/discovery/' + id);
        dmsg.className = 'msg'; dmsg.textContent = j.state === 'running' ? 'Taranıyor: ' + j.done + ' / ' + j.total + ' adres, ' + j.hosts.length + ' makine bulundu…' : (j.state === 'done' ? 'Tarama bitti: ' + j.hosts.length + ' makine.' : 'Tarama iptal edildi.');
        if (j.state === 'running') { setTimeout(() => { if (location.hash === '#ag') poll(id); }, 1500); return; }
        dbtn.disabled = false; showHosts(j);
      } catch (e) { dbtn.disabled = false; dmsg.className = 'msg error'; dmsg.textContent = e.message; }
    };
    dform.addEventListener('submit', async (e) => {
      e.preventDefault(); dout.replaceChildren(); dbtn.disabled = true;
      try { const r = await api('/api/discovery', { method: 'POST', body: { cidr: cidr.value.trim() } }); poll(r.id); }
      catch (err) { dbtn.disabled = false; dmsg.className = 'msg error'; dmsg.textContent = err.message; }
    });
    const discSection = ME.role === 'admin' ? [h('h2', null, 'Ağ keşfi'),
      h('p', { class: 'muted small' }, 'Verilen aralıktaki makineleri ping ve yaygın portlarla (22, 80, 135, 139, 443, 445, 1433, 3306, 3389, 5432, 8080, 8443) tarar; bulunanlardan seçtiklerinizi tek tıkla izlemeye eklersiniz. Yalnızca kendi ağınızı ve yetkili olduğunuz aralıkları tarayın; tarama güvenlik sistemlerinde alarm üretebilir. SaaS modunda kapalıdır.'),
      dform, dmsg, dout] : [];
    let traps = { enabled: false, traps: [] };
    try { traps = await api('/api/traps'); } catch (e) { /* yetki yok */ }
    const trapRows = traps.traps.map((t) => h('tr', null,
      h('td', { class: 'mono small' }, new Date(t.at).toLocaleString((window.LOCALE || 'tr-TR'))), h('td', { class: 'mono' }, t.source), h('td', null, t.asset || '—'),
      h('td', { class: 'mono small' }, t.trap_oid || '—'),
      h('td', { class: 'small' }, (t.vars || []).slice(0, 6).map((v) => v.OID + '=' + v.Value).join(' · '))));
    return h('section', null, h('h2', null, 'Yol izleme'),
      h('p', { class: 'muted small' }, 'Bu sunucudan hedefe giden ağ yolunu TTL ile çıkarır; her sıçrama için adres, gecikme ve paket kaybını gösterir. Yalnızca yetkili olduğunuz hedeflerde kullanın. Sürekli izlemek ve rota değişiminde alarm almak için Probe\'lar sayfasında "Yol izleme" türünde probe ekleyin.'),
      form, msg, out,
      ...discSection,
      h('h2', null, 'SNMP trap\'leri'),
      h('p', { class: 'muted small' }, traps.enabled ? 'Trap alıcısı açık. Cihazlarda trap hedefini bu sunucunun UDP trap portuna ve APM_SNMP_TRAP_COMMUNITY değerine ayarlayın; kaynak IP bir probe hedefiyle eşleşirse trap o asset\'e bağlanır ve snmp.trap metriği yazılır.' : 'Trap alıcısı kapalı. Açmak için APM_SNMP_TRAP_ADDR (ör. :1162) ve APM_SNMP_TRAP_COMMUNITY ortam değişkenlerini tanımlayıp sunucuyu yeniden başlatın.'),
      table(['Zaman', 'Kaynak', 'Asset', 'Trap OID', 'Değişkenler'], trapRows, 'Henüz trap alınmadı.', PAGE));
  }

  // ---- Topoloji haritası ----
  let selectedNode = null;
  let showAllTopo = null; // null: otomatik (40 asset'e kadar hepsi gösterilir)
  let tv169 = false, tvTimer = null;
  try { tv169 = localStorage.getItem('apm_tv169') === '1'; } catch (e) { tv169 = false; }
  // 16:9 görünüm: sabit 1920x1080 sahne, ekrana sığacak şekilde ölçeklenir (duvar ekranı / TV).
  function fitTv() {
    const st = document.querySelector('.tv-stage'); if (!st) return;
    const k = Math.min(innerWidth / 1920, innerHeight / 1080);
    st.style.transform = `translate(-50%, -50%) scale(${k})`;
  }
  function setTv(on) {
    tv169 = on;
    try { localStorage.setItem('apm_tv169', on ? '1' : '0'); } catch (e) { /* yoksay */ }
    document.body.classList.toggle('tv', on);
    if (on) { try { document.documentElement.requestFullscreen && document.documentElement.requestFullscreen(); } catch (e) { /* yoksay */ } }
    else { clearInterval(tvTimer); try { document.fullscreenElement && document.exitFullscreen(); } catch (e) { /* yoksay */ } }
    render();
  }
  window.addEventListener('resize', fitTv);
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && tv169 && !document.fullscreenElement) setTv(false); });
  document.addEventListener('fullscreenchange', () => { if (!document.fullscreenElement && tv169) setTv(false); });
  const SVGNS = 'http://www.w3.org/2000/svg';
  function s(tag, attrs, ...kids) {
    const el = document.createElementNS(SVGNS, tag);
    for (const [k, v] of Object.entries(attrs || {})) if (v != null && v !== false) el.setAttribute(k, v);
    for (const kid of kids.flat()) if (kid != null) el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
    return el;
  }

  const NW = 176, NH = 48, GX = 28, GY = 88, PAD = 24;

  // ---- Tür bazlı şekiller (topoloji ve küçük simgeler) ----
  // Her şekil, NW×NH (176×48) kutuya çizilir. dx/dy metnin kaydırması, box ana kontur, deco süs çizgileridir.
  const SHAPES = {
    host: { dx: 0, dy: 0, draw: (w, hh) => [s('rect', { class: 'box', width: w, height: hh, rx: 5 }),
      s('path', { class: 'deco', d: `M${w - 38} 14h26M${w - 38} 24h26M${w - 38} 34h26` })] },
    database: { dx: 0, dy: 6, draw: (w, hh) => [s('path', { class: 'box', d: `M0 8a${w / 2} 8 0 0 1 ${w} 0v${hh - 16}a${w / 2} 8 0 0 1 -${w} 0z` }),
      s('path', { class: 'deco', d: `M0 8a${w / 2} 8 0 0 0 ${w} 0` })] },
    website: { dx: 0, dy: 7, draw: (w, hh) => [s('rect', { class: 'box', width: w, height: hh, rx: 6 }),
      s('path', { class: 'deco', d: `M0 15H${w}` }), s('circle', { class: 'deco dot2', cx: 10, cy: 7.5, r: 2 }), s('circle', { class: 'deco dot2', cx: 18, cy: 7.5, r: 2 }), s('circle', { class: 'deco dot2', cx: 26, cy: 7.5, r: 2 })] },
    webservice: { dx: 8, dy: 0, draw: (w, hh) => [s('rect', { class: 'box', width: w, height: hh, rx: hh / 2 }),
      s('text', { class: 'glyph', x: w - 26, y: hh / 2 + 4, 'text-anchor': 'middle' }, '</>')] },
    network: { dx: 10, dy: 0, draw: (w, hh) => [s('polygon', { class: 'box', points: `0,${hh / 2} 16,0 ${w - 16},0 ${w},${hh / 2} ${w - 16},${hh} 16,${hh}` })] },
    cache: { dx: 4, dy: 0, draw: (w, hh) => [s('polygon', { class: 'box', points: `10,0 ${w - 10},0 ${w},10 ${w},${hh - 10} ${w - 10},${hh} 10,${hh} 0,${hh - 10} 0,10` }),
      s('path', { class: 'deco', d: `M${w - 22} 12l-6 12h8l-6 12` })] },
    queue: { dx: 12, dy: 0, draw: (w, hh) => [s('polygon', { class: 'box', points: `14,0 ${w},0 ${w - 14},${hh} 0,${hh}` })] },
    service: { dx: 0, dy: 0, draw: (w, hh) => [s('rect', { class: 'box', width: w, height: hh, rx: 7 })] },
    external: { dx: 4, dy: 0, draw: (w, hh) => [s('rect', { class: 'box', width: w, height: hh, rx: hh / 2 })] },
  };
  SHAPES.url = SHAPES.website;
  const shapeOf = (kind) => SHAPES[kind] || SHAPES.service;

  // Küçük simge (listelerde ve açıklamada): aynı şekil, ölçeklenmiş.
  function kindIcon(kind, width = 34) {
    const sh = shapeOf(kind);
    return s('svg', { class: 'kindicon', viewBox: '0 0 44 34', width, height: Math.round(width * 34 / 44), 'aria-hidden': 'true' },
      ...sh.draw(44, 34).filter((e) => e.tagName !== 'text'));
  }


  // Hiyerarşi üstten alta: en üstte kimseye bağımlı olmayan altyapı (sunucu, ağ), onun altında üzerinde çalışanlar
  // ve ona bağımlı olanlar. Üstteki çalışmazsa altındakiler etkilenir.
  function layoutTopology(nodes, edgesAll) {
    const edges = edgesAll.filter((e) => e.kind !== 'flow'); // ağ akışı bağlantıları yerleşimi etkilemez
    const layer = new Map(nodes.map((n) => [n.id, 0]));
    for (let i = 0; i < nodes.length; i++) {
      let changed = false;
      for (const e of edges) {
        const want = layer.get(e.target) + 1;
        if (layer.has(e.source) && layer.has(e.target) && want > layer.get(e.source) && want < nodes.length) { layer.set(e.source, want); changed = true; }
      }
      if (!changed) break; // döngü varsa katman sayısı sınırlı kalır
    }
    // Hiçbir bağlantısı olmayan asset'ler hiyerarşinin altında ızgara halinde toplanır; geniş katmanlar satırlara bölünür.
    const linked = new Set(); edges.forEach((e) => { linked.add(e.source); linked.add(e.target); });
    const MAXC = 8;
    const layers = [];
    for (const n of nodes) { if (!linked.has(n.id)) continue; const l = layer.get(n.id); (layers[l] = layers[l] || []).push(n); }
    const rows = layers.filter(Boolean);
    const index = new Map();
    rows.forEach((row, ri) => {
      if (ri > 0) { // çizgi kesişimlerini azaltmak için bağımlı olunanların ortalama konumuna göre sırala
        const score = (n) => {
          const ps = edges.filter((e) => e.source === n.id && index.has(e.target)).map((e) => index.get(e.target));
          return ps.length ? ps.reduce((a, b) => a + b, 0) / ps.length : 1e9;
        };
        row.sort((a, b) => score(a) - score(b) || a.name.localeCompare(b.name));
      } else row.sort((a, b) => a.name.localeCompare(b.name));
      row.forEach((n, i) => index.set(n.id, i));
    });
    const lines = [];
    const chunk = (list) => { for (let i = 0; i < list.length; i += MAXC) lines.push(list.slice(i, i + MAXC)); };
    rows.forEach(chunk);
    const iso = nodes.filter((n) => !linked.has(n.id)).sort((a, b) => a.name.localeCompare(b.name));
    const hierLines = lines.length;
    chunk(iso);
    const widest = Math.max(1, ...lines.map((r) => r.length));
    const width = PAD * 2 + widest * NW + (widest - 1) * GX;
    const pos = new Map();
    lines.forEach((row, ri) => {
      const rowW = row.length * NW + (row.length - 1) * GX;
      const x0 = (width - rowW) / 2;
      const y = PAD + ri * (NH + GY) + (ri >= hierLines && hierLines ? 24 : 0); // bağlantısız grup biraz aşağıdan başlar
      row.forEach((n, i) => pos.set(n.id, { x: x0 + i * (NW + GX), y }));
    });
    return { pos, width, height: PAD * 2 + lines.length * NH + (lines.length - 1) * GY };
  }

  // ---- Etkileşimli harita: sürükleme, odaklanıp yakınlaşma, kutu içi istatistikler ----
  const POS_KEY = 'apm_topo_pos2'; // yerleşim yönü değişti (üstte altyapı): eski kayıtlı konumlar kullanılmaz
  const CARD_H = 70;
  function loadPos() { try { return JSON.parse(localStorage.getItem(POS_KEY) || '{}') || {}; } catch (e) { return {}; } }
  function savePos(m) { try { localStorage.setItem(POS_KEY, JSON.stringify(m)); } catch (e) { /* depolama kapalı: yalnızca bu oturumda */ } }
  function resetPos() { try { localStorage.removeItem(POS_KEY); } catch (e) { /* yoksay */ } }

  // id'ye bağımlı olanlar (yukarı) ve id'nin bağımlı olduğu her şey (aşağı), geçişli olarak.
  function connected(edges, id) {
    const up = new Set([id]), down = new Set([id]);
    for (let ch = true; ch;) {
      ch = false;
      for (const e of edges) {
        if (up.has(e.target) && !up.has(e.source)) { up.add(e.source); ch = true; }
        if (down.has(e.source) && !down.has(e.target)) { down.add(e.target); ch = true; }
      }
    }
    return { all: new Set([...up, ...down]), dependents: up.size - 1, dependencies: down.size - 1 };
  }

  const pct = (v) => (v == null ? '—' : (v >= 99.95 ? v.toFixed(1) : v.toFixed(2)) + '%');
  function ageText(ts) {
    if (!ts) return '—';
    const sec = Math.max(0, (Date.now() - new Date(ts)) / 1000);
    return sec < 90 ? Math.round(sec) + ' sn önce' : sec < 5400 ? Math.round(sec / 60) + ' dk önce' : Math.round(sec / 3600) + ' sa önce';
  }
  // Kutunun altına eklenen istatistik satırları [etiket, değer].
  function statLines(n, st) {
    st = st || {};
    if (n.kind === 'external') return [['İstek', String(n.requests)], ['Ağ hatası', String(n.failed)], ['HTTP 4xx/5xx', String(n.errors)]];
    const lines = [['Erişilebilirlik 24 sa', pct(st.avail_24h)], ['Gecikme (1 sa)', st.latency_ms == null ? '—' : fmt(st.latency_ms) + ' ms']];
    if (st.cpu != null || st.mem != null) lines.push(['CPU / Bellek / Disk', [st.cpu, st.mem, st.disk].map((v) => (v == null ? '–' : Math.round(v))).join(' / ') + '%']);
    else lines.push(['Son veri', ageText(st.last_seen)]);
    lines.push(['Açık alarm', String(n.open_alerts || 0)]);
    return lines;
  }

  function edgePath(a, ha, b, hb) {
    const acx = a.x + NW / 2, bcx = b.x + NW / 2;
    if (b.y >= a.y + ha + 10) { const my = (a.y + ha + b.y) / 2; return `M${acx} ${a.y + ha} C${acx} ${my} ${bcx} ${my} ${bcx} ${b.y}`; }
    if (a.y >= b.y + hb + 10) { const my = (a.y + b.y + hb) / 2; return `M${acx} ${a.y} C${acx} ${my} ${bcx} ${my} ${bcx} ${b.y + hb}`; }
    const right = b.x > a.x, sx = right ? a.x + NW : a.x, tx = right ? b.x : b.x + NW, mx = (sx + tx) / 2, ay = a.y + NH / 2, by = b.y + NH / 2;
    return `M${sx} ${ay} C${mx} ${ay} ${mx} ${by} ${tx} ${by}`;
  }

  // opts: {focus, stats, height, onSelect(id)}; dönen svg'de setFocus(id|null) bulunur.
  const nstat = (n) => (n.impacted ? 'down' : n.status);
  function drawTopology(t, opts) {
    opts = opts || {};
    const { pos: auto } = layoutTopology(t.nodes, t.edges);
    const saved = loadPos();
    const pos = new Map();
    for (const n of t.nodes) {
      const sp = saved[String(n.id)];
      pos.set(n.id, sp && isFinite(sp.x) && isFinite(sp.y) ? { x: Math.max(0, sp.x), y: Math.max(0, sp.y) } : { ...auto.get(n.id) });
    }
    const byId = new Map(t.nodes.map((n) => [n.id, n]));
    const roots = new Set(t.root_causes);
    const H = opts.height || 520;
    let focus = byId.has(opts.focus) ? opts.focus : null;
    let fset = focus != null ? connected(t.edges, focus).all : null;
    const hOf = (id) => NH + (fset && fset.has(id) ? CARD_H : 0);
    // webOnly: varsayılan olarak yalnızca web uygulamaları görünür; biri seçilince bağlı bileşenler de açılır.
    const isWeb = (id) => ['website', 'webservice', 'url'].includes((byId.get(id) || {}).kind);
    const shown = (id) => !opts.webOnly || isWeb(id) || !!(fset && fset.has(id));
    const webIds = opts.webOnly ? new Set(t.nodes.filter((n) => isWeb(n.id)).map((n) => n.id)) : null;

    const svg = s('svg', { class: 'topo-svg', width: '100%', height: H, role: 'group', 'aria-label': 'Topoloji haritası (sürükleyerek yerleştirebilirsiniz)' },
      s('defs', null,
        ...[['topo-arrow', 'arrow'], ['topo-arrow-hi', 'arrow hi']].map(([id, cls]) =>
          s('marker', { id, viewBox: '0 0 10 10', refX: '9', refY: '5', markerWidth: '7', markerHeight: '7', orient: 'auto-start-reverse' }, s('path', { d: 'M0 0L10 5L0 10z', class: cls })))));

    const edgeEls = t.edges.map((e) => { const el = s('path', { class: 'edge' + (e.kind === 'runs_on' ? ' runs' : '') + (e.kind === 'flow' ? ' flowedge' : '') }); svg.append(el); return { e, el }; });
    const nodeEls = new Map();

    const bounds = (ids) => {
      let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
      for (const [id, p] of pos) {
        if (ids && !ids.has(id)) continue;
        x0 = Math.min(x0, p.x); y0 = Math.min(y0, p.y); x1 = Math.max(x1, p.x + NW); y1 = Math.max(y1, p.y + hOf(id));
      }
      return { x0, y0, x1, y1 };
    };
    // Görünüm kutusu: odak varsa bağlı bileşenler, yoksa tüm harita. Kapsayıcı oranına uyarlanır.
    const targetBox = () => {
      const b = bounds(fset || (webIds && webIds.size ? webIds : null)), pad = fset ? 48 : PAD;
      let w = Math.max(b.x1 - b.x0 + pad * 2, fset ? 560 : 0), hh = Math.max(b.y1 - b.y0 + pad * 2, fset ? 300 : 0);
      const ratio = (svg.clientWidth || 1100) / H;
      if (w / hh < ratio) w = hh * ratio; else hh = w / ratio;
      return { x: (b.x0 + b.x1) / 2 - w / 2, y: (b.y0 + b.y1) / 2 - hh / 2, w, h: hh };
    };
    let vb = null, anim = 0;
    const setVB = (v) => { vb = v; svg.setAttribute('viewBox', `${v.x.toFixed(1)} ${v.y.toFixed(1)} ${v.w.toFixed(1)} ${v.h.toFixed(1)}`); };
    const fly = (to, ms) => {
      cancelAnimationFrame(anim);
      if (!vb || ms === 0 || matchMedia('(prefers-reduced-motion: reduce)').matches) { setVB(to); return; }
      const from = vb, t0 = performance.now();
      const step = (now) => {
        const k = Math.min(1, (now - t0) / ms), e = k < .5 ? 2 * k * k : 1 - Math.pow(-2 * k + 2, 2) / 2;
        setVB({ x: from.x + (to.x - from.x) * e, y: from.y + (to.y - from.y) * e, w: from.w + (to.w - from.w) * e, h: from.h + (to.h - from.h) * e });
        if (k < 1) anim = requestAnimationFrame(step);
      };
      anim = requestAnimationFrame(step);
    };

    const placeEdges = () => {
      for (const { e, el } of edgeEls) {
        const a = pos.get(e.source), b = pos.get(e.target);
        if (!a || !b) continue;
        el.setAttribute('d', edgePath(a, hOf(e.source), b, hOf(e.target)));
        const hi = focus != null && (e.source === focus || e.target === focus);
        const faded = fset && !(fset.has(e.source) && fset.has(e.target));
        const hid = !(shown(e.source) && shown(e.target));
        el.setAttribute('class', 'edge' + (e.kind === 'runs_on' ? ' runs' : '') + (e.kind === 'flow' ? ' flowedge' : '') + (hi ? ' hi' : '') + (faded ? ' faded' : '') + (hid ? ' hid' : ''));
        el.setAttribute('marker-end', `url(#${hi ? 'topo-arrow-hi' : 'topo-arrow'})`);
      }
    };
    const place = (id) => { const g = nodeEls.get(id), p = pos.get(id); g.setAttribute('transform', `translate(${p.x.toFixed(1)} ${p.y.toFixed(1)})`); };

    const build = (n) => {
      const sh = shapeOf(n.kind), root = roots.has(n.id), focused = fset && fset.has(n.id);
      const name = n.name.length > 22 ? n.name.slice(0, 21) + '…' : n.name;
      const kids = [s('title', null, `${n.name} (${STATUS[nstat(n)]})${n.impacted ? ' · bağımlı olduğu çalışmıyor: ' + (n.impacted_by || []).join(', ') : ''}`),
        root ? s('rect', { class: 'ring', x: -5, y: -5, width: NW + 10, height: hOf(n.id) + 10, rx: 9 }) : null,
        ...sh.draw(NW, NH),
        s('circle', { class: 'dot', cx: 14 + sh.dx, cy: 17 + sh.dy, r: 4.5 }),
        s('text', { x: 26 + sh.dx, y: 21 + sh.dy }, name),
        s('text', { class: 'sub' + (root || n.impacted ? ' rc' : ''), x: 14 + sh.dx, y: 38 + sh.dy - (n.kind === 'website' ? 2 : 0) }, root ? 'olası kök neden' : n.impacted ? '← ' + (n.impacted_by || []).join(', ').slice(0, 18) : n.kind === 'external' ? `dış · ${n.requests} istek${n.failed ? ' · ' + n.failed + ' hata' : ''}` : `${KIND[n.kind] || n.kind}${n.language !== 'none' ? ' · ' + LANG[n.language] : ''}`)];
      if (focused) {
        const lines = statLines(n, (opts.stats || {})[String(n.id)]);
        kids.push(s('rect', { class: 'card', x: 0, y: NH + 4, width: NW, height: CARD_H - 4, rx: 6 }));
        lines.forEach(([k, v], i) => kids.push(s('text', { class: 'st', x: 10, y: NH + 20 + i * 14.5 }, k), s('text', { class: 'sv', x: NW - 10, y: NH + 20 + i * 14.5, 'text-anchor': 'end' }, v)));
      }
      if (n.page_changed) kids.push(s('g', { class: 'chgtag' }, s('rect', { x: NW - 84, y: -9, width: 82, height: 16, rx: 3 }), s('text', { x: NW - 43, y: 3, 'text-anchor': 'middle' }, 'sayfa değişti')));
      if (n.open_alerts) kids.push(s('g', { class: 'badge' }, s('circle', { cx: NW - 4, cy: 2, r: 10 }), s('text', { x: NW - 4, y: 6, 'text-anchor': 'middle' }, n.open_alerts)));
      const faded = fset && !focused;
      const g = s('g', { class: `node ${nstat(n)}${n.impacted ? ' impacted' : ''}${root ? ' root' : ''}${n.id === focus ? ' sel' : ''}${n.kind === 'external' ? ' ext' : ''}${n.page_changed ? ' chg' : ''}${faded ? ' faded' : ''}${shown(n.id) ? '' : ' hid'}`, tabindex: faded || !shown(n.id) ? '-1' : '0', role: 'button',
        'aria-label': `${n.name}, ${STATUS[nstat(n)]}${root ? ', olası kök neden' : ''}${n.impacted ? ', bağımlı olduğu çalışmıyor' : ''}`, transform: `translate(${pos.get(n.id).x} ${pos.get(n.id).y})` }, ...kids);
      attach(g, n);
      return g;
    };
    const persist = (id) => { const m = loadPos(); m[String(id)] = { x: Math.round(pos.get(id).x), y: Math.round(pos.get(id).y) }; savePos(m); };
    const toSvg = (ev) => { const p = svg.createSVGPoint(); p.x = ev.clientX; p.y = ev.clientY; return p.matrixTransform(svg.getScreenCTM().inverse()); };
    function attach(g, n) {
      g.addEventListener('pointerdown', (ev) => {
        if (ev.button !== 0) return;
        const start = toSvg(ev), p0 = { ...pos.get(n.id) };
        let moved = false;
        g.setPointerCapture(ev.pointerId);
        const move = (e) => {
          if (!moved && Math.hypot(e.clientX - ev.clientX, e.clientY - ev.clientY) < 4) return;
          if (!moved) { moved = true; g.classList.add('dragging'); cancelAnimationFrame(anim); }
          const q = toSvg(e);
          pos.set(n.id, { x: Math.max(0, p0.x + q.x - start.x), y: Math.max(0, p0.y + q.y - start.y) });
          place(n.id); placeEdges();
        };
        const up = () => {
          g.removeEventListener('pointermove', move); g.removeEventListener('pointerup', up); g.removeEventListener('pointercancel', up);
          g.classList.remove('dragging');
          if (moved) { persist(n.id); if (!fset) fly(targetBox(), 200); } else if (opts.onSelect) opts.onSelect(n.id);
        };
        g.addEventListener('pointermove', move); g.addEventListener('pointerup', up); g.addEventListener('pointercancel', up);
      });
      g.addEventListener('keydown', (ev) => {
        const d = { ArrowLeft: [-14, 0], ArrowRight: [14, 0], ArrowUp: [0, -14], ArrowDown: [0, 14] }[ev.key];
        if (d) { ev.preventDefault(); const p = pos.get(n.id); pos.set(n.id, { x: Math.max(0, p.x + d[0]), y: Math.max(0, p.y + d[1]) }); place(n.id); placeEdges(); persist(n.id); }
        else if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); if (opts.onSelect) opts.onSelect(n.id); }
      });
    }

    const paintNodes = () => {
      for (const n of t.nodes) {
        const g = build(n), old = nodeEls.get(n.id);
        if (old) old.replaceWith(g); else svg.append(g);
        nodeEls.set(n.id, g);
      }
    };
    paintNodes(); placeEdges();
    // İlk çizimde tüm haritayı kurup (genişlik ölçülebilsin diye) bir sonraki karede odağa uçar.
    setVB({ x: 0, y: 0, w: 1100, h: H });
    const initial = () => {
      const full = (() => { const f = fset; fset = null; const b = targetBox(); fset = f; return b; })();
      setVB(full);
      if (fset) requestAnimationFrame(() => fly(targetBox(), 450));
    };
    requestAnimationFrame(initial);
    svg.setFocus = (id) => {
      focus = byId.has(id) ? id : null;
      fset = focus != null ? connected(t.edges, focus).all : null;
      paintNodes(); placeEdges(); fly(targetBox(), 450);
    };
    svg.connected = (id) => connected(t.edges, id);
    // Yakınlaştırma: (cx,cy) noktası sabit kalır; f>1 yakınlaştırır. Sınır: tüm haritanın 1/8'i ile 4 katı arası.
    svg.zoom = (f, cx, cy) => {
      if (!vb) return;
      cancelAnimationFrame(anim);
      const full = (() => { const o = fset; fset = null; const b = targetBox(); fset = o; return b; })();
      const w = Math.min(full.w * 4, Math.max(full.w / 8, vb.w / f)), k = w / vb.w;
      const px = cx == null ? vb.x + vb.w / 2 : cx, py = cy == null ? vb.y + vb.h / 2 : cy;
      setVB({ x: px - (px - vb.x) * k, y: py - (py - vb.y) * k, w, h: vb.h * k });
    };
    svg.fit = () => { cancelAnimationFrame(anim); fly(targetBox(), 250); };
    svg.addEventListener('wheel', (ev) => {
      if (!ev.ctrlKey && !ev.metaKey) return; // normal kaydırma sayfayı kaydırır; Ctrl + tekerlek yakınlaştırır
      ev.preventDefault();
      const q = toSvg(ev);
      svg.zoom(ev.deltaY < 0 ? 1.15 : 1 / 1.15, q.x, q.y);
    }, { passive: false });
    // Boş alana basıp sürükleyerek haritayı kaydır.
    svg.addEventListener('pointerdown', (ev) => {
      if (ev.button !== 0 || ev.target !== svg) return;
      const start = { x: ev.clientX, y: ev.clientY }, v0 = { ...vb };
      const sc = () => v0.w / (svg.clientWidth || 1100);
      svg.setPointerCapture(ev.pointerId); svg.classList.add('panning'); cancelAnimationFrame(anim);
      const move = (e) => setVB({ x: v0.x - (e.clientX - start.x) * sc(), y: v0.y - (e.clientY - start.y) * sc(), w: v0.w, h: v0.h });
      const up = () => { svg.classList.remove('panning'); svg.removeEventListener('pointermove', move); svg.removeEventListener('pointerup', up); svg.removeEventListener('pointercancel', up); };
      svg.addEventListener('pointermove', move); svg.addEventListener('pointerup', up); svg.addEventListener('pointercancel', up);
    });
    return svg;
  }

  function extPanel(t, n, mine) {
    const names = new Map(t.nodes.map((x) => [x.id, x.name]));
    return h('div', { class: 'grid' },
      h('div', { class: 'panel' }, h('div', { class: 'row' }, h('h3', null, n.name), pill(n.status, STATUS[n.status])),
        h('p', { class: 'muted small' }, 'Tarayıcı senaryolarının çalışırken eriştiği, otomatik keşfedilen dış bağımlılık.'),
        h('div', { class: 'facts' }, h('div', { class: 'fact' }, h('span', null, 'İstek sayısı (son çalıştırma)'), h('b', null, n.requests)),
          h('div', { class: 'fact' }, h('span', null, 'Ağ hatası'), h('b', { class: n.failed ? 'error' : '' }, n.failed)),
          h('div', { class: 'fact' }, h('span', null, 'HTTP 4xx/5xx yanıtı'), h('b', null, n.errors)))),
      h('div', { class: 'panel' }, h('h3', null, 'Bu alan adını kullananlar'),
        h('div', { class: 'facts' }, mine.map((e) => h('div', { class: 'fact' }, h('span', null, names.get(e.source)))))));
  }

  // Seçilen asset'in açık alarmları, hatalı probe/senaryoları ve sayfa değişikliği uyarıları.
  async function topoIssues(n) {
    const [alerts, probes, scs] = await Promise.all([
      api('/api/alerts?state=open').catch(() => []), api('/api/probes').catch(() => []), api('/api/scenarios').catch(() => [])]);
    const al = (Array.isArray(alerts) ? alerts : alerts.alerts || []).filter((a) => a.asset_id === n.id);
    const bp = (Array.isArray(probes) ? probes : probes.probes || []).filter((p) => p.asset_id === n.id && p.last_error);
    const sc = (Array.isArray(scs) ? scs : scs.scenarios || []).filter((x) => x.asset_id === n.id);
    const bs = sc.filter((x) => x.last_error);
    const chg = sc.filter((x) => x.last_changed_at && Date.now() - new Date(x.last_changed_at) < 864e5);
    if (!al.length && !bp.length && !bs.length && !chg.length) return null;
    const rows = [];
    al.forEach((a) => rows.push(h('div', { class: 'fact' }, h('span', null, pill(a.severity, SEV[a.severity] || a.severity), ' ', h('b', null, a.rule),
      h('div', { class: 'muted small mono' }, `${a.metric} ${a.comparator} ${a.threshold} (şu an: ${fmt(a.value)}) · ${ago(a.opened_at)}${a.silenced ? ' · bakımda' : ''}${a.ack_by ? ' · üstlenen: ' + a.ack_by : ''}`)))));
    bp.forEach((p) => rows.push(h('div', { class: 'fact' }, h('span', null, pill('down', 'Probe hatası'), ' ', h('b', null, p.type + ' ' + p.target),
      h('div', { class: 'error small mono' }, p.last_error)))));
    bs.forEach((x) => rows.push(h('div', { class: 'fact' }, h('span', null, pill('down', 'Senaryo hatası'), ' ', h('b', null, x.name),
      h('div', { class: 'error small mono' }, x.last_error)))));
    chg.forEach((x) => rows.push(h('div', { class: 'fact' }, h('span', null, pill('warning', 'Sayfa değişti'), ' ', h('b', null, x.name), ' ', h('span', { class: 'muted small' }, ago(x.last_changed_at))),
      h('button', { type: 'button', class: 'small', onclick: () => showChanges(x) }, 'Ayrıntılar'))));
    return h('div', { class: 'panel issues' }, h('h3', null, `Hata ve uyarılar (${rows.length})`), h('div', { class: 'facts' }, rows));
  }

  async function topoPanel(t, id) {
    const n = t.nodes.find((x) => x.id === id);
    if (typeof id === 'string') return extPanel(t, n, t.edges.filter((e) => e.target === id));
    const names = new Map(t.nodes.map((x) => [x.id, x.name]));
    const imp = await api(`/api/assets/${id}/impact`);
    const list = (items, empty) => items.length
      ? h('div', { class: 'facts' }, items.map((i) => h('div', { class: 'fact' }, h('span', null, i.name, h('span', { class: 'muted small mono' }, `  ${i.depth}. seviye`)), pill(i.status, STATUS[i.status]))))
      : h('p', { class: 'muted' }, empty);
    const mine = t.edges.filter((e) => e.source === id || e.target === id);
    const issues = await topoIssues(n);
    return h('div', { class: 'grid' }, issues,
      h('div', { class: 'panel' }, h('div', { class: 'row' }, h('h3', null, n.name), pill(n.status, STATUS[n.status]), h('button', { type: 'button', onclick: () => openCard(id) }, 'Asset kartı')),
        h('p', { class: 'muted small' }, `${KIND[n.kind] || n.kind}, ${n.collection === 'agent' ? 'agent' : 'agent\'sız'}${n.open_alerts ? `, ${n.open_alerts} açık alarm` : ''}`),
        h('h3', null, `Bu asset düşerse etkilenenler (${imp.impacted.length})`), list(imp.impacted, 'Ona bağımlı asset yok.')),
      h('div', { class: 'panel' }, h('h3', null, `Bağımlılıkları (${imp.dependencies.length})`), list(imp.dependencies, 'Bağımlı olduğu asset yok.')),
      h('div', { class: 'panel' }, h('h3', null, 'Bağlantılar'),
        mine.length ? h('div', { class: 'facts' }, mine.map((e) => h('div', { class: 'fact' },
          h('span', null, `${names.get(e.source)} → ${names.get(e.target)}`, h('span', { class: 'muted small mono' }, e.kind === 'runs_on' ? '  üzerinde çalışır' : '  bağımlı')),
          e.auto ? h('span', { class: 'muted small' }, 'otomatik') : h('button', { type: 'button', class: 'danger', onclick: async () => { try { await api('/api/topology/edges/' + e.id, { method: 'DELETE' }); render(); } catch (x) { alert(x.message); } } }, 'Sil'))))
          : h('p', { class: 'muted' }, 'Bu asset\'in bağlantısı yok.')));
  }

  // Hata iletisinden olası neden: senaryo ve dış bağımlılık hatalarını sınıflandırır.
  function errorCause(msg) {
    const m = (msg || '').toLowerCase();
    const R = (cls, head, text) => ({ cls, head, text });
    if (/bot koruması|captcha|cloudflare|access denied|erişim engellendi/.test(m)) return R('warning', 'Bot koruması engelliyor', 'Hedef site otomatik tarayıcıyı engelliyor; sitede sorun olmayabilir. İzleme sunucusunun IP adresi için site sahibinden izin (allowlist) isteyin ya da bu sayfayı HTTP probe ile izleyin.');
    if (/err_name_not_resolved|no such host|dns/.test(m)) return R('down', 'Ad çözümlenemiyor', 'Alan adı IP adresine çevrilemedi. DNS kaydı silinmiş/değişmiş olabilir ya da izleme sunucusunun kullandığı çözümleyici yanıt vermiyor. Performans › DNS sekmesinden kaydı kontrol edin.');
    if (/err_cert|x509|certificate|sertifika/.test(m)) return R('down', 'Sertifika sorunu', 'TLS sertifikası geçersiz: süresi dolmuş, alan adıyla uyuşmuyor ya da kurum içi CA tanınmıyor olabilir. Sertifika tarihini ve zincirini kontrol edin; kurum içi CA için certs/ca.pem tanımlayın.');
    if (/err_connection_refused|connection refused|err_connection_reset|err_connection_closed|err_empty_response/.test(m)) return R('down', 'Bağlantı reddedildi', 'Sunucuya ulaşıldı ama bağlantı kabul edilmedi ya da kesildi. Servis durmuş, port kapalı ya da güvenlik duvarı/yük dengeleyici bağlantıyı düşürüyor olabilir.');
    if (/err_address_unreachable|err_internet_disconnected|network is unreachable|no route|err_proxy|err_tunnel/.test(m)) return R('down', 'Ağ erişimi yok', 'İzleme sunucusundan hedefe ağ yolu yok. Güvenlik duvarı kuralı, vekil (proxy) ayarı veya yönlendirme sorunu olabilir; Yol izleme ile nerede kesildiğine bakın.');
    if (/timeout|timed out|zaman aşımı|deadline|err_timed_out/.test(m)) return R('down', 'Zaman aşımı', 'Sayfa ya da beklenen öğe süresinde gelmedi. Sunucu yavaş yanıt veriyor, bir dış kaynak takılıyor ya da sayfa yapısı değiştiği için beklenen öğe hiç görünmüyor olabilir. Gecikme kırılımına ve adım sürelerine bakın.');
    if (/giriş|login|oturum|parola|401|unauthorized/.test(m)) return R('warning', 'Giriş/oturum sorunu', 'Senaryo giriş yapamadı ya da oturum düştü. Parola değişmiş, hesap kilitlenmiş, giriş formu değişmiş ya da ek doğrulama (MFA/CAPTCHA) istenmiş olabilir. Senaryonun kimlik bilgilerini ve giriş adımlarını doğrulayın.');
    if (/öğe bulunamadı|not found.*selector|selector|tıklanamadı|doldurulamadı/.test(m)) return R('warning', 'Sayfa yapısı değişmiş', 'Senaryonun aradığı düğme/alan sayfada yok. Çoğunlukla arayüz güncellemesinden kaynaklanır; site çalışıyor olabilir. Senaryoyu yeniden kaydedin ya da ilgili adımın seçicisini güncelleyin.');
    if (/\b403\b|forbidden|yasak/.test(m)) return R('warning', 'Erişim yasak (403)', 'Sunucu isteği reddetti. IP kısıtı, WAF kuralı ya da yetkisiz kullanıcı olabilir.');
    if (/\b404\b/.test(m)) return R('warning', 'Sayfa bulunamadı (404)', 'Adres artık yok; bağlantı değişmiş ya da sayfa kaldırılmış olabilir. Senaryodaki adresi güncelleyin.');
    if (/\b5\d\d\b|internal server error|bad gateway|service unavailable|gateway timeout/.test(m)) return R('down', 'Sunucu hatası (5xx)', 'Uygulama ya da arkasındaki servis hata döndürüyor. Uygulama günlüklerine, son dağıtıma ve bağımlı servis/veritabanı durumuna bakın.');
    if (/sayfa betiği|javascript|script/.test(m)) return R('warning', 'Sayfa betiği hatası', 'Sayfadaki JavaScript hata verdi; bir betik dosyası yüklenememiş ya da son sürümde hata olabilir.');
    if (/sayfa açılamadı|sayfa yüklenemedi|err_/.test(m)) return R('down', 'Sayfa yüklenemedi', 'Tarayıcı sayfayı açamadı. Hedefin ayakta olduğunu, adresin doğru olduğunu ve izleme sunucusundan erişilebildiğini kontrol edin.');
    return R('warning', 'Neden belirlenemedi', 'İleti bilinen bir kalıba uymuyor. Senaryonun son çalıştırma adımlarına ve ekran görüntüsüne bakın.');
  }
  // Dış bağımlılık (alan adı) yorumu: ağ hatası ve HTTP hata yanıtı sayılarına göre.
  function hostCause(n) {
    const req = n.requests || 0, f = n.failed || 0, e = n.errors || 0;
    if (f && req && f >= req) return { cls: 'down', head: 'Hiç ulaşılamıyor', text: 'Bu alan adına giden isteklerin tamamı ağ düzeyinde başarısız. Alan adı çözümlenemiyor, servis kapalı ya da izleme sunucusunun çıkışı güvenlik duvarı/vekil tarafından engelleniyor olabilir. Reklam/analitik alan adıysa engellenmesi sayfayı bozmayabilir.' };
    if (f) return { cls: 'down', head: 'Bazı istekler ağda başarısız', text: `${req} istekten ${f} tanesi yanıt alınamadan kesildi. Aralıklı bağlantı sorunu, zaman aşımı ya da isteğin sayfa değişirken iptal edilmesi olabilir; sürerse bu servisin erişilebilirliğini ayrı bir probe ile izleyin.` };
    if (e && req && e / req >= 0.5) return { cls: 'warning', head: 'Çoğu yanıt hata kodu', text: `${req} istekten ${e} tanesi HTTP 4xx/5xx döndü. Kaynak kaldırılmış (404), erişim yetkisi yok (401/403) ya da servis hata veriyor (5xx) olabilir.` };
    if (e) return { cls: 'warning', head: 'Hata kodu dönen istekler var', text: `${e} istek HTTP 4xx/5xx döndü; eksik bir görsel/betik (404) gibi küçük bir sorun olabilir. Sayfa işlevini etkileyip etkilemediğini kontrol edin.` };
    return { cls: 'up', head: 'Sağlıklı', text: 'Bu alan adına giden isteklerde ağ hatası ya da hata kodu yok.' };
  }
  const insightBox = (ins) => h('div', { class: 'timing-note' }, pill(ins.cls, ins.head), h('div', null, ins.text));
  // Senaryo bağımlılık haritasında seçilen kutunun ayrıntısı: hata iletisi + olası neden.
  async function scenDetail(sv, id) {
    const n = sv.nodes.find((x) => x.id === id);
    const names = new Map(sv.nodes.map((x) => [x.id, x.name]));
    const scsRaw = await api('/api/scenarios').catch(() => []);
    const scs = Array.isArray(scsRaw) ? scsRaw : scsRaw.scenarios || [];
    const scRow = (x) => h('div', { class: 'fact' }, h('span', null, x.last_error ? pill('down', 'Senaryo hatası') : pill('up', 'Başarılı'), ' ', h('b', null, x.name), x.last_run ? h('span', { class: 'muted small' }, ' · ' + ago(x.last_run)) : null,
      x.last_error ? h('div', { class: 'error small mono' }, x.last_error) : null, x.last_error ? insightBox(errorCause(x.last_error)) : null));
    if (typeof id === 'string') {
      const users = sv.edges.filter((e) => e.target === id).map((e) => e.source);
      const related = scs.filter((x) => users.includes(x.asset_id) && x.last_error && x.last_error.toLowerCase().includes(n.name.toLowerCase()));
      return h('div', { class: 'grid' },
        h('div', { class: 'panel' }, h('div', { class: 'row' }, h('h3', null, n.name), pill(n.status, STATUS[n.status])),
          h('div', { class: 'facts' }, h('div', { class: 'fact' }, h('span', null, 'İstek sayısı (son çalıştırma)'), h('b', null, n.requests)),
            h('div', { class: 'fact' }, h('span', null, 'Ağ hatası'), h('b', { class: n.failed ? 'error' : '' }, n.failed)),
            h('div', { class: 'fact' }, h('span', null, 'HTTP 4xx/5xx yanıtı'), h('b', null, n.errors))),
          h('h3', null, 'Yorum'), insightBox(hostCause(n))),
        h('div', { class: 'panel' }, h('h3', null, 'Bu alan adını kullananlar'),
          h('div', { class: 'facts' }, users.map((u) => h('div', { class: 'fact' }, h('span', null, names.get(u))))),
          related.length ? h('h3', null, 'Bu alan adını anan senaryo hataları') : null, related.length ? h('div', { class: 'facts' }, related.map(scRow)) : null));
    }
    const mine = scs.filter((x) => x.asset_id === id);
    const failing = mine.filter((x) => x.last_error);
    const hosts = sv.edges.filter((e) => e.source === id).map((e) => sv.nodes.find((x) => x.id === e.target)).filter(Boolean);
    const badHosts = hosts.filter((x) => x.failed || x.errors).sort((a, b) => (b.failed - a.failed) || (b.errors - a.errors));
    return h('div', { class: 'grid' },
      h('div', { class: 'panel' }, h('div', { class: 'row' }, h('h3', null, n.name), pill(n.status, STATUS[n.status])),
        h('p', { class: 'muted small' }, `${mine.length} senaryo · ${hosts.length} dış bağımlılık` + (failing.length ? ` · ${failing.length} hatalı senaryo` : '')),
        mine.length ? h('div', { class: 'facts' }, [...failing, ...mine.filter((x) => !x.last_error)].map(scRow)) : h('p', { class: 'muted' }, 'Bu varlığa bağlı senaryo bulunamadı.'),
        !failing.length && !badHosts.length && mine.length ? insightBox({ cls: 'up', head: 'Sağlıklı', text: 'Senaryolar başarılı ve dış bağımlılıklarda hata yok.' }) : null),
      h('div', { class: 'panel' }, h('h3', null, `Sorunlu dış bağımlılıklar (${badHosts.length})`),
        badHosts.length ? h('div', { class: 'facts' }, badHosts.map((x) => h('div', { class: 'fact' }, h('span', null, h('b', null, x.name), h('span', { class: 'muted small mono' }, `  ${x.requests} istek · ${x.failed} ağ hatası · ${x.errors} HTTP hata`), insightBox(hostCause(x))))))
          : h('p', { class: 'muted' }, 'Dış bağımlılıkların hiçbirinde ağ hatası ya da hata kodu yok.')));
  }

  async function viewTopoMap() {
    await refreshHeader();
    let [t, stats] = await Promise.all([api('/api/topology'), api('/api/topology/stats').catch(() => ({}))]);
    if (selectedNode && !t.nodes.some((n) => n.id === selectedNode)) selectedNode = null;
    const names = new Map(t.nodes.map((n) => [n.id, n.name]));
    const head = h('section', null, h('h2', null, 'Topoloji'),
      h('p', { class: 'muted' }, 'Hiyerarşi üstten alta doğrudur: en üstte altyapı (sunucu, ağ), altında üzerinde çalışan veya ona bağımlı uygulamalar ve veritabanları bulunur. Ok, bir asset\'ten bağımlı olduğu asset\'e gider. Üstteki çalışmazsa altındakiler de kırmızı görünür.'));
    if (!t.nodes.length) {
      return h('div', null, head, h('p', { class: 'panel muted' }, 'Henüz asset yok. Asset\'ler eklendikten sonra aralarındaki bağımlılıkları buradan tanımlayın.'));
    }
    const banner = t.root_causes.length
      ? h('div', { class: 'panel rc-banner' }, h('b', null, 'Olası kök neden: '), t.root_causes.slice(0, 12).map((id) => names.get(id)).join(', '),
          t.root_causes.length > 12 ? h('span', { class: 'muted' }, ` ve ${t.root_causes.length - 12} tane daha`) : null,
          h('span', { class: 'muted' }, '. Sağlıksız asset\'lerden, bağımlı olduğu hiçbir asset sağlıksız olmayanlar.'))
      : h('div', { class: 'panel muted' }, 'Sağlıksız asset yok; kök neden aranmıyor.');
    const nImp = t.nodes.filter((n) => n.impacted).length;
    const legend = h('div', { class: 'legend' }, ['up', 'degraded', 'down', 'offline', 'unknown'].map((k) => h('span', { class: 'lg ' + k }, STATUS[k])),
      h('span', { class: 'lg rootlg' }, 'Olası kök neden'), h('span', { class: 'lg down' }, 'Etkileniyor: bağımlı olduğu çalışmıyor' + (nImp ? ` (${nImp})` : '')), h('span', null, 'Kesikli ok: üzerinde çalışır'), h('span', null, '"sayfa değişti": son 24 saatte sayfa yapısı değişti'));
    const shapesLegend = h('div', { class: 'legend shapes' }, ['website', 'webservice', 'service', 'database', 'host', 'network', 'cache', 'queue', 'external']
      .filter((k) => t.nodes.some((n) => n.kind === k) || k !== 'external').map((k) => h('span', { class: 'kl' }, kindIcon(k, 38), KIND[k])));

    const panel = h('section', { id: 'topo-panel' });
    // Senaryoların keşfettiği dış servisler ana haritadan ayrılır, aşağıda ayrı bir haritada gösterilir.
    const isScenExt = (n) => n.kind === 'external' && n.collection === 'discovered';
    const scenAll = t;
    const scenNodes = new Set(t.nodes.filter(isScenExt).map((n) => n.id));
    const scenAssets = new Set(t.edges.filter((e) => e.auto && scenNodes.has(e.target)).map((e) => e.source));
    t = { ...t, nodes: t.nodes.filter((n) => !isScenExt(n)), edges: t.edges.filter((e) => !scenNodes.has(e.target)) };
    // Varsayılan görünüm: yalnızca ana cihazlar (sunucu/ağ). Bir cihaz seçilince o, bağlıları ve üzerinde çalışanlar.
    const isMain = (n) => n.kind === 'host' || n.kind === 'network';
    const hasMain = t.nodes.some(isMain);
    let vt = t, viewNote = null;
    const allOn = showAllTopo == null ? t.nodes.length <= 40 : showAllTopo;
    if (!allOn && (hasMain || selectedNode != null)) {
      const keep = new Set();
      if (selectedNode == null) t.nodes.filter(isMain).forEach((n) => keep.add(n.id));
      else {
        // Seçilen asset'e herhangi bir yolla (her iki yönde, dolaylı olanlar dahil) bağlı olan her şey.
        keep.add(selectedNode);
        for (let grow = true; grow;) { grow = false; t.edges.forEach((e) => { if (keep.has(e.source) !== keep.has(e.target)) { keep.add(e.source); keep.add(e.target); grow = true; } }); }
      }
      vt = { ...t, nodes: t.nodes.filter((n) => keep.has(n.id)), edges: t.edges.filter((e) => keep.has(e.source) && keep.has(e.target)) };
      viewNote = selectedNode == null
        ? `Yalnızca ana cihazlar gösteriliyor (${vt.nodes.length}/${t.nodes.length}). Ayrıntı için bir cihaza tıklayın.`
        : `Seçili asset ve ona doğrudan/dolaylı bağlı her şey gösteriliyor (${vt.nodes.length}/${t.nodes.length}).`;
    }
    if (TOPO.flows && t.flow_edges && t.flow_edges.length) {
      const have = new Set(vt.nodes.map((n) => n.id));
      vt = { ...vt, edges: vt.edges.concat(t.flow_edges.filter((e) => have.has(e.source) && have.has(e.target))) };
    }
    const map = drawTopology(vt, { focus: selectedNode, stats, height: 760, onSelect: (id) => { selectedNode = selectedNode === id ? null : id; render(); } });
    const zb = (label, title, fn) => h('button', { type: 'button', class: 'small', title, 'aria-label': title, onclick: fn }, label);
    const wrap = h('div', { class: 'topo-wrap' }, map,
      h('div', { class: 'topo-zoom', role: 'group', 'aria-label': 'Yakınlaştırma' }, zb('+', 'Yakınlaştır', () => map.zoom(1.3)), zb('−', 'Uzaklaştır', () => map.zoom(1 / 1.3)), zb('Sığdır', 'Tüm haritayı sığdır', () => map.fit())));
    const tools = h('div', { class: 'row tools' }, h('span', { class: 'muted small' }, 'Kutuları sürükleyerek yerleştirebilirsiniz (bu tarayıcıda saklanır). Boş alanı sürükleyerek kaydırın; +/− düğmeleri veya Ctrl + tekerlek ile yakınlaştırın. Bir kutuya tıklayınca bağlı bileşenlere yakınlaşılır ve istatistikler görünür.'),
      (() => {
        const pickOpts = t.nodes.filter((n) => typeof n.id === 'number').sort((a, b) => a.name.localeCompare(b.name, 'tr')).map((n) => [String(n.id), `${n.name} (${KIND[n.kind] || n.kind})`]);
        const sel = pickOpts.length > 30 ? combo('topo-pick', pickOpts, selectedNode)
          : (() => { const e = h('select', { id: 'topo-pick', 'aria-label': 'Hizmet seç' }, h('option', { value: '' }, 'Hizmet seç… (tüm bağlıları gelsin)'), pickOpts.map(([v, tx]) => h('option', { value: v }, tx))); e.value = selectedNode == null ? '' : String(selectedNode); return e; })();
        sel.addEventListener('change', () => { const v = comboValue(sel); selectedNode = v ? Number(v) : null; render(); });
        return h('label', { class: 'small topo-pick' }, 'Hizmet: ', sel);
      })(),
      viewNote ? h('span', { class: 'small' }, viewNote) : null,
      h('span', { class: 'btns' }, selectedNode != null ? h('button', { type: 'button', onclick: () => { selectedNode = null; render(); } }, hasMain && !allOn ? 'Ana cihazlara dön' : 'Tümünü göster') : null,
        hasMain ? h('button', { type: 'button', onclick: () => { showAllTopo = !allOn; render(); } }, allOn ? 'Yalnızca ana cihazlar' : 'Tüm asset\'leri göster') : null,
        (t.flow_edges && t.flow_edges.length) ? h('button', { type: 'button', 'aria-pressed': String(!!TOPO.flows), title: 'NetFlow/IPFIX verisinden öğrenilen konuşmaları noktalı çizgiyle gösterir (bağımlılık sayılmaz)', onclick: () => { TOPO.flows = !TOPO.flows; render(); } }, TOPO.flows ? 'Ağ akışlarını gizle' : `Ağ akışlarını göster (${t.flow_edges.length})`) : null,
        h('button', { type: 'button', 'aria-pressed': String(!!TOPO.paused), title: 'Çok sayıda asset varken harita her 10-30 sn yeniden çizilir; incelerken durdurun', onclick: () => { TOPO.paused = !TOPO.paused; render(); } }, TOPO.paused ? 'Otomatik yenilemeyi başlat' : 'Otomatik yenilemeyi durdur'),
        h('button', { type: 'button', onclick: () => { resetPos(); render(); } }, 'Yerleşimi sıfırla')));
    if (selectedNode) panel.append(await topoPanel(t, selectedNode));
    else panel.append(h('p', { class: 'muted' }, 'Etkisini ve bağımlılıklarını görmek için bir asset\'e tıklayın.'));

    const msg = h('p', { class: 'msg', role: 'status' });
    const opts = t.nodes.filter((n) => n.kind !== 'external').map((n) => [String(n.id), n.name]);
    const form = opts.length < 2 ? h('p', { class: 'muted' }, 'Bağlantı tanımlamak için en az iki asset gerekir.')
      : h('form', { class: 'inline' },
        field('Bağımlı olan (kaynak)', combo('e-src', opts)),
        field('Bağımlı olunan (hedef)', combo('e-dst', opts, opts[1][0])),
        field('İlişki', select('e-kind', [['depends_on', 'bağımlıdır'], ['runs_on', 'üzerinde çalışır']])),
        h('button', { type: 'submit', class: 'primary' }, 'Bağlantı ekle'));
    if (opts.length >= 2) {
      form.addEventListener('submit', async (e) => {
        e.preventDefault();
        msg.className = 'msg'; msg.textContent = '';
        try {
          await api('/api/topology/edges', { method: 'POST', body: { source: Number(comboValue($('#e-src', form))), target: Number(comboValue($('#e-dst', form))), kind: $('#e-kind', form).value } });
          render();
        } catch (err) { msg.className = 'msg error'; msg.textContent = err.message; }
      });
    }
    // Tüm bağlantılar: değiştir / sil
    const manual = t.edges.filter((e) => !e.auto);
    const kindLabel = (k) => (k === 'runs_on' ? 'üzerinde çalışır' : 'bağımlıdır');
    const emsg = h('p', { class: 'msg', role: 'status' });
    const edgeRows = manual.map((e) => {
      const tr = h('tr', null);
      const showView = () => { tr.className = ''; tr.replaceChildren(
        h('td', null, names.get(e.source)), h('td', null, kindLabel(e.kind)), h('td', null, names.get(e.target)),
        h('td', { class: 'actions' },
          h('button', { type: 'button', onclick: showEdit }, 'Değiştir'),
          h('button', { type: 'button', class: 'danger', onclick: async () => {
            if (!confirm(`${names.get(e.source)} → ${names.get(e.target)} bağlantısı silinsin mi?`)) return;
            try { await api('/api/topology/edges/' + e.id, { method: 'DELETE' }); render(); } catch (x) { emsg.className = 'msg error'; emsg.textContent = x.message; }
          } }, 'Sil'))); };
      const showEdit = () => {
        const a = select(null, opts, String(e.source)), k = select(null, [['depends_on', 'bağımlıdır'], ['runs_on', 'üzerinde çalışır']], e.kind), b = select(null, opts, String(e.target));
        tr.className = 'editing-row';
        tr.replaceChildren(h('td', null, a), h('td', null, k), h('td', null, b), h('td', { class: 'actions' },
          h('button', { type: 'button', class: 'primary', onclick: async () => {
            try { await api('/api/topology/edges/' + e.id, { method: 'PUT', body: { source: Number(a.value), target: Number(b.value), kind: k.value } }); render(); } catch (x) { emsg.className = 'msg error'; emsg.textContent = x.message; }
          } }, 'Kaydet'),
          h('button', { type: 'button', onclick: showView }, 'Vazgeç')));
      };
      showView();
      return tr;
    });
    const edgeTable = manual.length
      ? table(['Kaynak', 'İlişki', 'Hedef', ''], edgeRows, '')
      : h('p', { class: 'muted' }, 'Elle tanımlanmış bağlantı yok. Otomatik bağlantılar (dış servis çağrıları) buradan değiştirilemez.');
    let scenSec = null;
    if (scenNodes.size) {
      const sv = { ...scenAll, nodes: scenAll.nodes.filter((n) => isScenExt(n) || scenAssets.has(n.id)), edges: scenAll.edges.filter((e) => e.auto && scenNodes.has(e.target)) };
      if (TOPO.scenSel != null && !sv.nodes.some((n) => n.id === TOPO.scenSel)) TOPO.scenSel = null;
      const detail = h('div', { class: 'scen-detail' });
      const showDetail = async () => {
        if (TOPO.scenSel == null) { detail.replaceChildren(h('p', { class: 'muted small' }, 'Bir kutuya tıklayın: o ve bağlı olduğu kutular yakınlaşır, varsa hata iletisi ve olası nedeni burada gösterilir. Aynı kutuya yeniden tıklamak tüm haritaya döner.')); return; }
        const want = TOPO.scenSel;
        detail.replaceChildren(h('p', { class: 'muted small' }, 'Yükleniyor…'));
        const el = await scenDetail(sv, want).catch((e) => h('p', { class: 'msg error' }, e.message));
        if (TOPO.scenSel === want) detail.replaceChildren(el);
      };
      const sm = drawTopology(sv, { stats, height: 420, focus: TOPO.scenSel, onSelect: (id) => { TOPO.scenSel = TOPO.scenSel === id ? null : id; sm.setFocus(TOPO.scenSel); showDetail(); } });
      showDetail();
      scenSec = h('section', null, h('h3', null, 'Senaryo bağımlılıkları'),
        h('p', { class: 'muted small' }, `Tarayıcı senaryolarının sayfa yüklerken keşfettiği dış servisler (${scenNodes.size}) ana haritadan ayrı gösterilir.`),
        h('div', { class: 'topo-wrap' }, sm), detail);
    }
    return h('div', null, head, banner, legend, shapesLegend, tools, wrap, panel, scenSec,
      h('section', null, h('h3', null, 'Bağlantılar'), h('div', { class: 'panel' }, edgeTable, emsg)),
      h('section', null, h('h3', null, 'Bağlantı tanımla'), h('div', { class: 'panel' }, form, msg)));
  }


  // Topoloji: Harita ve Senaryolar ayrı sekmelerde (senaryo formu otomatik yenilemede bozulmasın diye Senaryolar sekmesinde yenileme yok).
  const TOPO = { tab: 'map', flows: false, scenSel: null };
  async function viewTopoloji() {
    return viewTopoMap();
  }

  // ---- Performans (ajansız APM): uygulamalar, yavaş sorgular, gecikme kırılımı, bağlantılar ----
  const PERF = { tab: 'apps', win: '1h', asset: null, sort: 'total_ms', db: null };
  // Ortak "Yorum" hücresi: durum etiketi + açıklama (+ isteğe bağlı soluk ayrıntı satırı).
  const insightCell = (ins, extra) => h('td', { class: 'small timing-note' }, pill(ins.cls, ins.head), h('div', null, ins.text), extra ? h('div', { class: 'muted' }, extra) : null);
  // Yüzdelik çubuk (0-100); hücre içinde küçük görsel pay göstergesi.
  const miniBar = (pct, tone) => h('span', { class: 'hopwrap' }, sized(h('span', { class: 'hopbar' + (tone ? ' ' + tone : '') }), Math.max(2, Math.min(100, pct))));
  const PERF_TABS = [['apps', 'Uygulamalar'], ['queries', 'Veritabanı sorguları'], ['timing', 'Gecikme kırılımı'], ['dns', 'DNS'], ['net', 'Ağ servisleri'], ['path', 'Yol izleme'], ['conns', 'Bağlantılar'], ['flows', 'Ağ akışı']];
  const pms = (v) => (v == null ? '—' : v >= 1000 ? (v / 1000).toFixed(2) + ' sn' : fmt(v) + ' ms');
  const ppct = (v) => (v == null ? '—' : v.toFixed(v < 10 ? 2 : 1) + '%');

  function perfCode(text) { return h('pre', { class: 'perf-code' }, h('code', null, text)); }

  function perfSetup(rc) {
    const addr = rc && rc.addr ? rc.addr : 'SUNUCU:1514';
    const host = addr.replace(/^(\[::\]|0\.0\.0\.0|):/, 'SUNUCU:');
    return h('details', { class: 'panel perf-setup' }, h('summary', null, 'Nasıl bağlanır? (nginx, Apache, HAProxy örnekleri)'),
      h('p', { class: 'muted small' }, 'Web sunucusu veya ters vekil, her istek için tek satır logu syslog ile APP_MON\'a gönderir. Hedef makineye ajan kurulmaz. "appmon_key" yerine "Lisans ve anahtarlar" sayfasından ürettiğiniz agent rollü bir anahtar yazın; "app" izlenen uygulamanın adıdır.'),
      h('h4', null, 'nginx'),
      perfCode(`log_format appmon 'appmon_key=APM_ANAHTARI app=uygulama-adi m=$request_method uri=$request_uri st=$status rt=$request_time';\naccess_log syslog:server=${host},tag=appmon appmon;`),
      h('h4', null, 'Apache (httpd)'),
      perfCode(`LogFormat "appmon_key=APM_ANAHTARI app=uygulama-adi m=%m uri=%U st=%>s rt_us=%D" appmon\nCustomLog "|/usr/bin/logger -n ${host.split(':')[0]} -P ${host.split(':')[1] || '1514'} -t appmon" appmon`),
      h('h4', null, 'HAProxy'),
      perfCode(`log-format "appmon_key=APM_ANAHTARI app=uygulama-adi m=%HM uri=%HPO st=%ST rt_ms=%Tr"`),
      h('p', { class: 'muted small' }, 'Sorgu dizesi (?…) saklanmaz; yoldaki sayısal kimlikler, UUID ve uzun belirteçler {id}/{uuid}/{token} olarak gruplanır. Güvenlik için aygıttan APP_MON\'a giden ağı özel tutun; anahtar düz metin gönderilir (UDP/TCP).'));
  }

  // Uygulama yorumu: hata oranı, gecikme düzeyi ve kuyruk (p95/ortalama) oranına bakar.
  function appInsight(a) {
    if (!a.reqs || a.reqs < 5) return { cls: 'unknown', head: 'Az veri', text: 'Örnek sayısı az; güvenilir bir yorum için daha fazla istek bekleyin.' };
    if (a.err_pct >= 5) return { cls: 'down', head: 'Hata oranı yüksek', text: 'İsteklerin önemli bölümü 5xx dönüyor. Uç nokta ayrıntısında hangi yolun hata verdiğine bakın; son dağıtım, bağımlı servis veya veritabanı kaynaklı olabilir.' };
    if (a.err_pct >= 1) return { cls: 'warning', head: 'Hatalar var', text: 'Bir kısım istek 5xx dönüyor. Hatanın tek bir uç noktada mı yoksa genel mi olduğunu uç nokta ayrıntısından kontrol edin.' };
    if ((a.p95_ms || 0) >= 2000) return { cls: 'down', head: 'Yanıt yavaş', text: 'İsteklerin %5\'i 2 sn\'den uzun sürüyor. Gecikme kırılımı ve Veritabanı sorguları sekmelerinden darboğazın ağda mı uygulamada mı olduğunu ayırın.' };
    if ((a.p95_ms || 0) >= 800 && (a.avg_ms || 0) > 0 && a.p95_ms > a.avg_ms * 4) return { cls: 'warning', head: 'Yavaş kuyruk', text: 'Çoğu istek hızlı ama bir kısmı belirgin yavaş (p95 ortalamanın çok üstünde). Genelde tek bir uç nokta, soğuk önbellek veya zaman aşımına yakın bağımlı çağrılar nedenidir.' };
    if ((a.p95_ms || 0) >= 800) return { cls: 'warning', head: 'Gecikme yüksek', text: 'Yanıt süreleri genel olarak yüksek. En çok süre tüketen uç noktaları ve veritabanı sorgularını inceleyin.' };
    return { cls: 'up', head: 'Sağlıklı', text: 'Hata oranı ve yanıt süreleri normal aralıkta; işlem gerekmiyor.' };
  }
  async function perfApps() {
    const d = await api('/api/perf/apps?window=' + PERF.win);
    const rc = d.receiver || {};
    const winSel = select(null, [['1h', 'Son 1 saat'], ['6h', 'Son 6 saat'], ['24h', 'Son 24 saat']], PERF.win);
    winSel.addEventListener('change', () => { PERF.win = winSel.value; render(); });
    const status = rc.enabled
      ? h('p', { class: 'muted small' }, `Alıcı açık: ${rc.addr} · kabul edilen ${rc.accepted} satır` + (rc.unauthorized ? ` · reddedilen ${rc.unauthorized}` : '') + (rc.invalid ? ` · geçersiz ${rc.invalid}` : '') + (rc.dropped ? ` · hız sınırı nedeniyle atılan ${rc.dropped}` : '') + (rc.last_at ? ` · son satır ${ago(new Date(rc.last_at * 1000).toISOString())}` : ''))
      : h('p', { class: 'muted small' }, 'Erişim günlüğü alıcısı kapalı. Açmak için sunucuda APM_SYSLOG_ADDR tanımlayın (ör. :1514); ayrıntı için Yardım › Performans (ajansız APM).');
    if (PERF.asset) return perfEndpoints(d, winSel);
    const maxP95 = Math.max(1, ...d.apps.map((a) => a.p95_ms || 0));
    const maxRpm = Math.max(0.0001, ...d.apps.map((a) => a.req_per_min || 0));
    const rows = d.apps.map((a) => h('tr', { class: 'clickrow', tabindex: '0' },
      h('td', null, h('b', null, a.app), a.source === 'trace' ? h('span', { class: 'muted small', title: 'Dağıtık izleme (trace) verisinden; ayrıntı için tıklayın' }, ' · trace') : null, a.untimed && a.untimed >= a.reqs ? h('span', { class: 'muted small', title: 'Erişim günlüğünde istek süresi yok; süre için nginx/Apache sunucusunda ajan -web-setup çalıştırın' }, ' · süre yok') : null,
        h('div', { class: 'muted small' }, `${a.reqs} istek · p50 ${pms(a.p50_ms)} · p99 ${pms(a.p99_ms)} · en yavaş ${pms(a.max_ms)}`)),
      h('td', { class: 'mono' }, fmt(a.req_per_min), miniBar(((a.req_per_min || 0) / maxRpm) * 100)),
      h('td', { class: 'mono' }, a.err_pct >= 1 ? pill(a.err_pct >= 5 ? 'down' : 'warning', ppct(a.err_pct)) : ppct(a.err_pct)),
      h('td', { class: 'mono' }, pms(a.avg_ms)), h('td', { class: 'mono' }, pms(a.p95_ms), miniBar(((a.p95_ms || 0) / maxP95) * 100)),
      insightCell(appInsight(a))));
    rows.forEach((tr, i) => { const go = () => { const ap = d.apps[i]; if (ap.source === 'trace') { TRACE.tab = 'services'; TRACE.service = ap.app; goTo('trace'); return; } PERF.asset = ap.asset_id; render(); }; tr.addEventListener('click', go); tr.addEventListener('keydown', (e) => { if (e.key === 'Enter') go(); }); });
    return h('div', { class: 'stack' }, h('div', { class: 'row' }, winSel), status,
      table(['Uygulama', 'İstek/dk', 'Hata (5xx)', 'Ort.', 'p95', 'Yorum'], rows, 'Bu aralıkta erişim günlüğü veya trace verisi yok. Ajan; makinedeki nginx/Apache/IIS erişim günlüklerini (SEND_WEBLOG) ve uygulamaların OpenTelemetry trace\'lerini (SEND_OTLP, Dağıtık izleme) gönderir. Ajanlar sayfasından ilgili veri türünün açık olduğunu kontrol edin.', PAGE),
      d.apps.length ? h('p', { class: 'muted small' }, 'Uç nokta ayrıntısı için bir satıra tıklayın; p50, p99 ve en yavaş değer uygulama adının altında. Çubuklar listedeki en yüksek değere göre orantılıdır. Yüzdelikler kova (histogram) üzerinden tahmin edilir.') : null,
      perfSetup(rc));
  }

  function sized(el, pct) { el.style.width = pct.toFixed(2) + '%'; return el; }

  function perfSeries(series) {
    if (!series || !series.length) return null;
    const w = 640, hgt = 90, pad = 4;
    const max = Math.max(1, ...series.map((x) => x.p95_ms || 0));
    const maxReq = Math.max(1, ...series.map((x) => x.reqs));
    const bw = Math.min(18, Math.max(2, (w - pad * 2) / series.length - 1));
    const ns = 'http://www.w3.org/2000/svg';
    const svg = document.createElementNS(ns, 'svg');
    svg.setAttribute('viewBox', `0 0 ${w} ${hgt}`); svg.setAttribute('class', 'perf-series'); svg.setAttribute('role', 'img');
    svg.setAttribute('aria-label', 'İstek sayısı (çubuk) ve p95 süre (çizgi)');
    const pts = [];
    series.forEach((x, i) => {
      const xx = pad + i * (bw + 1);
      const bh = (x.reqs / maxReq) * (hgt - 10);
      const r = document.createElementNS(ns, 'rect');
      r.setAttribute('x', xx); r.setAttribute('y', hgt - bh); r.setAttribute('width', bw); r.setAttribute('height', bh); r.setAttribute('class', x.err5xx ? 'ps-bar ps-err' : 'ps-bar');
      const t = document.createElementNS(ns, 'title'); t.textContent = `${new Date(x.t).toLocaleTimeString((window.LOCALE || 'tr-TR'), { hour: '2-digit', minute: '2-digit' })} · ${x.reqs} istek · p95 ${pms(x.p95_ms)}${x.err5xx ? ' · ' + x.err5xx + ' hata' : ''}`;
      r.append(t); svg.append(r);
      pts.push(`${(xx + bw / 2).toFixed(1)},${(hgt - 4 - ((x.p95_ms || 0) / max) * (hgt - 14)).toFixed(1)}`);
    });
    const pl = document.createElementNS(ns, 'polyline');
    pl.setAttribute('points', pts.join(' ')); pl.setAttribute('class', 'ps-line'); svg.append(pl);
    return h('div', { class: 'panel' }, h('div', { class: 'muted small' }, `Çubuklar: istek sayısı (en çok ${maxReq}); çizgi: p95 süre (en çok ${pms(max)}); kırmızı çubuk: 5xx hatası olan dilim.`), svg);
  }

  async function perfEndpoints(d, winSel) {
    const e = await api('/api/perf/endpoints?asset=' + PERF.asset + '&window=' + PERF.win);
    const back = h('button', { type: 'button', onclick: () => { PERF.asset = null; render(); } }, '← Tüm uygulamalar');
    const totalMs = e.endpoints.reduce((a, x) => a + x.total_ms, 0) || 1;
    const rows = e.endpoints.map((x) => h('tr', null,
      h('td', { class: 'mono' }, x.endpoint), h('td', { class: 'mono' }, String(x.reqs)),
      h('td', { class: 'mono' }, x.err5xx ? pill('down', `${x.err5xx} (${ppct(x.err_pct)})`) : '0'), h('td', { class: 'mono' }, x.err4xx ? String(x.err4xx) : '0'),
      h('td', { class: 'mono' }, pms(x.avg_ms)), h('td', { class: 'mono' }, pms(x.p50_ms)), h('td', { class: 'mono' }, pms(x.p95_ms)), h('td', { class: 'mono' }, pms(x.p99_ms)), h('td', { class: 'mono' }, pms(x.max_ms)),
      h('td', { class: 'mono' }, h('span', { class: 'hopwrap' }, sized(h('span', { class: 'hopbar' }), Math.max(2, (x.total_ms / totalMs) * 100))), ' ' + ((x.total_ms / totalMs) * 100).toFixed(0) + '%')));
    return h('div', { class: 'stack' }, h('div', { class: 'row' }, back, winSel), h('h3', null, e.app),
      perfSeries(e.series),
      table(['Uç nokta', 'İstek', 'Hata (5xx)', '4xx', 'Ort.', 'p50', 'p95', 'p99', 'En yavaş', 'Toplam süre payı'], rows, 'Bu uygulama için uç nokta verisi yok.', PAGE),
      h('p', { class: 'muted small' }, 'Sıralama: toplam süreye en çok katkı yapan uç nokta en üstte. Aynı yolun sayısal kimlikleri {id} olarak gruplanır; bir uygulamada en çok 300 farklı uç nokta izlenir, fazlası "OTHER /*" olarak toplanır.'));
  }

  // Sorgu yorumu: yavaşlama eğilimi, yavaş sorgu ve toplam süreye baskınlık.
  function queryInsight(q, share) {
    const rec = q.recent_mean_ms;
    if (q.slow && rec != null && q.mean_ms > 0 && rec > q.mean_ms * 1.5) return { cls: 'down', head: 'Yavaşlıyor', text: 'Son dönem ortalaması genel ortalamanın belirgin üstünde; veri büyümesi, eksik/kullanılmayan indeks veya kilit beklemesi olabilir. EXPLAIN (ANALYZE) ile planı inceleyin.' };
    if (q.slow) return { cls: 'warning', head: 'Yavaş sorgu', text: 'Ortalama süre eşiğin üzerinde. Plan tarama (Seq Scan) mı yapıyor, uygun indeks var mı, kontrol edin.' };
    if (share >= 40) return { cls: 'warning', head: 'Toplam süreyi domine ediyor', text: 'Tek başına veritabanı toplam süresinin büyük bölümünü tüketiyor; çağrı başına hızlı olsa da çağrı sayısını azaltmak (önbellek, toplu sorgu) en çok kazancı getirir.' };
    if (rec != null && q.mean_ms > 0 && rec > q.mean_ms * 2 && rec >= 20) return { cls: 'warning', head: 'Son dönemde yavaşladı', text: 'Son dönem ortalaması genel ortalamanın iki katından fazla; eğilimi izleyin.' };
    return { cls: 'up', head: 'Normal', text: 'Süre ve çağrı sayısı dengeli.' };
  }
  // Veritabanı özeti yorumu.
  function dbInsight(g) {
    if (g.slowN && g.worstTrend) return { cls: 'down', head: 'Sorgular yavaşlıyor', text: `${g.slowN} yavaş sorgu var ve en az biri son dönemde daha da yavaşladı. Ayrıntı için satıra tıklayın.` };
    if (g.slowN) return { cls: 'warning', head: `${g.slowN} yavaş sorgu`, text: 'Eşiğin üzerinde ortalama süreli sorgular var; ayrıntıda en çok süre alan sorgulardan başlayın.' };
    if (g.topShare >= 50) return { cls: 'warning', head: 'Tek sorgu baskın', text: 'Toplam sürenin yarısından fazlası tek bir sorguda; onu iyileştirmek veritabanı yükünü en çok azaltır.' };
    return { cls: 'up', head: 'Sağlıklı', text: 'Yavaş sorgu yok; süre dağılımı dengeli.' };
  }

  async function perfQueries() {
    const d = await api('/api/perf/queries');
    const srcs = d.sources.map((x) => h('li', null, x.asset + ': ', x.available ? pill('up', 'pg_stat_statements okunuyor') : pill('warning', 'kullanılamıyor'), ' ', x.available ? h('span', { class: 'muted small' }, ago(x.updated_at)) : h('span', { class: 'muted small' }, x.note || '')));
    const note = h('p', { class: 'muted small' }, `Yavaş sorgu: ortalaması ${d.slow_ms} ms ve üzeri. Kaynak: PostgreSQL pg_stat_statements; "PostgreSQL sağlık" (pgstats) probe'u 5 dakikada bir okur. Sorgu metinleri parametresizdir ($1, $2…), değerler görünmez. "Son dönem" son okumadan beri olan çağrıların ortalamasıdır.`);
    if (!d.sources.length) {
      return h('div', { class: 'stack' }, note, h('div', { class: 'panel muted' }, 'Henüz sorgu verisi yok. Ajanla: veritabanı makinesindeki ajanın yapılandırmasına APM_PG_DSN (PostgreSQL bağlantısı) ekleyin ve Ajanlar sayfasında "Veritabanı sorgu istatistikleri" veri türünü açın. Ajansız: Probe\'lar sayfasından "PostgreSQL sağlık (pgstats)" türünde bir probe ekleyin; hedef veritabanında pg_stat_statements uzantısı etkin olmalıdır (shared_preload_libraries ve CREATE EXTENSION pg_stat_statements).'));
    }
    // Veritabanına göre grupla
    const groups = new Map();
    d.queries.forEach((q) => { const k = q.asset; if (!groups.has(k)) groups.set(k, { asset: k, qs: [], total: 0, slowN: 0, worstMean: 0, worstTrend: false, topTotal: 0 }); const g = groups.get(k); g.qs.push(q); g.total += q.total_ms || 0; if (q.slow) g.slowN++; g.worstMean = Math.max(g.worstMean, q.mean_ms || 0); g.topTotal = Math.max(g.topTotal, q.total_ms || 0); if (q.slow && q.recent_mean_ms != null && q.mean_ms > 0 && q.recent_mean_ms > q.mean_ms * 1.5) g.worstTrend = true; });
    groups.forEach((g) => { g.topShare = g.total ? (g.topTotal / g.total) * 100 : 0; });
    const srcList = h('details', { class: 'panel' }, h('summary', null, `Kaynaklar (${d.sources.length})`), h('ul', { class: 'perf-src' }, srcs));
    // Ayrıntı: seçili veritabanının sorguları
    if (PERF.db && groups.has(PERF.db)) {
      const g = groups.get(PERF.db);
      const back = h('button', { type: 'button', onclick: () => { PERF.db = null; render(); } }, '← Tüm veritabanları');
      const sortSel = select(null, [['total_ms', 'Toplam süre'], ['mean_ms', 'Ortalama süre'], ['recent_mean_ms', 'Son dönem ortalaması'], ['calls', 'Çağrı sayısı']], PERF.sort);
      sortSel.addEventListener('change', () => { PERF.sort = sortSel.value; render(); });
      const list = [...g.qs].sort((a, b) => (b[PERF.sort] || 0) - (a[PERF.sort] || 0));
      const rows = list.map((q) => {
        const share = g.total ? (q.total_ms / g.total) * 100 : 0;
        return h('tr', null,
          h('td', null, h('code', { class: 'perf-sql', title: q.query }, q.query.length > 160 ? q.query.slice(0, 160) + '…' : q.query),
            h('div', { class: 'muted small' }, `en yavaş ${pms(q.max_ms)}` + (q.recent_mean_ms == null ? '' : ` · son dönem ${pms(q.recent_mean_ms)} (${q.recent_calls} çağrı)`))),
          h('td', { class: 'mono' }, String(q.calls)), h('td', { class: 'mono' }, pms(q.mean_ms)),
          h('td', { class: 'mono' }, pms(q.total_ms), miniBar(share, q.slow ? 'bad' : ''), h('span', { class: 'muted small' }, ' ' + share.toFixed(0) + '%')),
          insightCell(queryInsight(q, share)));
      });
      return h('div', { class: 'stack' }, h('div', { class: 'row' }, back, h('label', null, 'Sırala ', sortSel)), h('h3', null, g.asset), note,
        table(['Sorgu', 'Çağrı', 'Ortalama', 'Toplam süre payı', 'Yorum'], rows, 'Sorgu verisi yok.', PAGE), srcList);
    }
    PERF.db = null;
    // Özet: veritabanı başına tek satır
    const all = [...groups.values()].sort((a, b) => b.total - a.total);
    const maxTotal = Math.max(1, ...all.map((g) => g.total));
    const rows = all.map((g) => {
      const tr = h('tr', { class: 'clickrow', tabindex: '0' },
        h('td', null, h('b', null, g.asset), h('div', { class: 'muted small' }, `${g.qs.length} sorgu`)),
        h('td', { class: 'mono' }, pms(g.total), miniBar((g.total / maxTotal) * 100)),
        h('td', { class: 'mono' }, g.slowN ? pill('down', String(g.slowN)) : '0'), h('td', { class: 'mono' }, pms(g.worstMean)),
        insightCell(dbInsight(g)));
      const go = () => { PERF.db = g.asset; render(); };
      tr.addEventListener('click', go); tr.addEventListener('keydown', (e) => { if (e.key === 'Enter') go(); });
      return tr;
    });
    return h('div', { class: 'stack' }, note,
      table(['Veritabanı', 'Toplam süre', 'Yavaş sorgu', 'En yüksek ortalama', 'Yorum'], rows, 'Henüz sorgu istatistiği toplanmadı; pg_stat_statements verisi birkaç dakika içinde gelir.', PAGE),
      h('p', { class: 'muted small' }, 'Sorgu listesi için bir veritabanına tıklayın.'), srcList);
  }

  const TIMING_PARTS = [['dns', 'DNS', 'c1'], ['connect', 'TCP bağlantısı', 'c2'], ['tls', 'TLS', 'c4'], ['ttfb', 'Sunucu yanıtı (ilk bayt)', 'c3'], ['transfer', 'İndirme', 'c7']];
  // Gecikme kırılımı yorumu: aşamaların payına ve mutlak süreye bakarak davranışı yorumlar (eşikler ms).
  const TIMING_LIMIT = { dns: 100, connect: 150, tls: 250, ttfb: 800, transfer: 500 };
  const TIMING_INSIGHT = {
    dns: ['Ad çözümleme gecikiyor', 'İstemci ad çözümlemesinde bekliyor; çözümleyici geç yanıt veriyor ya da kayıt önbelleğe alınmıyor olabilir. DNS sunucusunun yanıt süresini ve kaydın TTL değerini kontrol edin.'],
    connect: ['Bağlantı kurulumu yavaş', 'TCP el sıkışması uzun sürüyor; genellikle ağ yolundaki gecikme, yük dengeleyici/güvenlik duvarı kuyruğu ya da hedefin uzak bölgede olması nedeniyle oluşur. Ağ yolunu ve hedefin doluluğunu inceleyin.'],
    tls: ['TLS el sıkışması yavaş', 'Şifreli oturum kurulumu gecikiyor; uzun sertifika zinciri, OCSP denetimi (stapling kapalı) veya sunucu tarafı CPU baskısı olası nedenlerdir. TLS oturum yeniden kullanımını ve sertifika zincirini gözden geçirin.'],
    ttfb: ['Sunucu işlemesi yavaş', 'Bağlantı hızlı kurulmuş ama ilk bayt geç geliyor: gecikme ağda değil uygulama katmanında. İş mantığı, veritabanı sorguları veya bağımlı servis çağrıları şüphelidir; Uygulamalar ve Veritabanı sorguları sekmelerinde ilgili uç noktayı inceleyin.'],
    transfer: ['Yanıt indirmesi uzun sürüyor', 'Sunucu hızlı yanıt vermiş ama içerik geç iniyor; yanıt gövdesi büyük ya da bant genişliği kısıtlı olabilir. Sıkıştırma (gzip/br), önbellekleme ve yanıt boyutunu azaltmayı değerlendirin.'],
    setup: ['Yeni bağlantı maliyeti yüksek', 'Süre ağırlıklı olarak bağlantı ve TLS kurulumunda harcanıyor, sunucu yanıtı hızlı. Bu desen bağlantı yeniden kullanımının (keep-alive, TLS oturum devamı) kapalı olduğunu ya da ağ yolunun uzun olduğunu gösterir.'],
    mixed: ['Gecikme tek aşamada toplanmıyor', 'Ağ ve sunucu aşamalarının hepsi birlikte yavaş; tek bir darboğaz yok. Önce sunucu yanıtına, sonra bağlantı süresine bakın ve ölçümün tek seferlik olup olmadığını sonraki ölçümlerle doğrulayın.'],
    none: ['Ölçüm yok', 'Bu hedef için henüz aşama süresi alınamadı; sonraki probe çalışmasını bekleyin.'],
    ok: ['Sağlıklı', 'Aşamalar dengeli ve toplam süre düşük; işlem gerekmiyor.'],
    watch: ['İzlenmeli', 'Toplam süre yüksek ama tek bir aşamada toplanmıyor; eğilimi izleyin, sürerse aşama bazında inceleyin.'],
  };
  function timingInsight(t) {
    const get = (k) => t[k] || 0;
    const total = t.total || TIMING_PARTS.reduce((a, [k]) => a + get(k), 0) || 1;
    let top = TIMING_PARTS[0][0];
    for (const [k] of TIMING_PARTS) if (get(k) > get(top)) top = k;
    const share = get(top) / total, setup = get('connect') + get('tls');
    let key;
    if (!TIMING_PARTS.some(([k]) => get(k) > 0)) key = 'none';
    else if (setup > 300 && setup / total >= 0.5 && get('ttfb') / total < 0.4) key = 'setup';
    else if (share > 0.5 && get(top) > TIMING_LIMIT[top]) key = top;
    else if (total > 1500) key = 'mixed';
    else key = total <= 500 ? 'ok' : 'watch';
    const cls = key === 'none' ? 'unknown' : key === 'ok' ? 'up' : (total > 1500 ? 'down' : 'warning');
    return { cls, head: TIMING_INSIGHT[key][0], text: TIMING_INSIGHT[key][1], top, share };
  }
  async function perfTiming() {
    const d = await api('/api/perf/timing');
    const legend = h('div', { class: 'perf-legend' }, TIMING_PARTS.map(([, n, c]) => h('span', null, h('i', { class: 'sw ' + c }), n)));
    const rows = d.probes.map((p) => {
      const t = p.timing, total = t.total || TIMING_PARTS.reduce((a, [k]) => a + (t[k] || 0), 0) || 1;
      const ins = timingInsight(t);
      const bar = h('div', { class: 'perf-stack', role: 'img', 'aria-label': TIMING_PARTS.map(([k, n]) => `${n} ${pms(t[k])}`).join(', ') },
        TIMING_PARTS.map(([k, n, c]) => { const w = ((t[k] || 0) / total) * 100; return w > 0 ? sized(h('span', { class: c, title: `${n}: ${pms(t[k])}` }), w) : null; }));
      return h('tr', null, h('td', null, p.asset, h('div', { class: 'muted small mono' }, p.target)), h('td', null, bar),
        h('td', { class: 'mono small' }, TIMING_PARTS.map(([k, n]) => `${n.split(' ')[0]} ${pms(t[k])}`).join(' · ')), h('td', { class: 'mono' }, pms(t.total)),
        h('td', { class: 'small timing-note' }, pill(ins.cls, ins.head), h('div', null, ins.text),
          h('div', { class: 'muted' }, 'En büyük pay: ', TIMING_PARTS.find(([k]) => k === ins.top)[1], ' %' + (ins.share * 100).toFixed(0))));
    });
    return h('div', { class: 'stack' },
      h('p', { class: 'muted small' }, 'HTTP probe\'larının son ölçümünde istek süresi aşamalara ayrılır; böylece yavaşlığın ağda mı, TLS\'te mi, sunucuda mı olduğu görülür. Ek probe ayarı gerekmez.'),
      legend, table(['Hedef', 'Dağılım', 'Aşamalar', 'Toplam', 'Yorum'], rows, 'Henüz kırılım verisi yok (HTTP probe\'ları çalıştıkça dolar).', PAGE));
  }

  // ---- Ağ servisleri: DNS, TLS, NTP, TCP, ICMP, UDP ve yol izleme (davranışsal yorumlar) ----
  const NET_WINS = [['1h', 'Son 1 saat'], ['24h', 'Son 24 saat'], ['7d', 'Son 7 gün']];
  const DSCP_NAMES = { 0: 'BE', 8: 'CS1', 10: 'AF11', 16: 'CS2', 18: 'AF21', 24: 'CS3', 26: 'AF31', 32: 'CS4', 34: 'AF41', 40: 'CS5', 46: 'EF', 48: 'CS6', 56: 'CS7' };
  const dscpName = (v) => (v == null ? '—' : v + (DSCP_NAMES[v] ? ' (' + DSCP_NAMES[v] + ')' : ''));
  const DSCP_CHOICES = [[0, 'İşaretsiz (BE)'], [8, 'CS1'], [18, 'AF21'], [26, 'AF31'], [34, 'AF41'], [24, 'CS3'], [40, 'CS5'], [46, 'EF (ses)'], [48, 'CS6']];
  const NONE_INS = { cls: 'unknown', head: 'Ölçüm yok', text: 'Probe henüz çalışmadı; ilk ölçümden sonra yorum görünür.' };

  // Ortak gecikme/erişilebilirlik yorumu: tür metinleri farklı, eşikler aynı mantıkla uygulanır.
  function latencyInsight(r, words) {
    const st = r.stats || {}, avg = st.avg_ms, p95 = st.p95_ms, up = st.up_pct, prev = st.prev_avg_ms;
    if (!r.last_run) return NONE_INS;
    if (up != null && up < 10) return { cls: 'down', head: words.down[0], text: words.down[1] };
    if (up != null && up < 95) return { cls: 'down', head: 'Kararsız erişim', text: 'Ölçümlerin bir kısmı başarısız; aralıklı ağ kaybı, servisin yeniden başlaması veya aşırı yük belirtisidir. Başarısızlıkların zamanına bakıp ağ ve servis günlükleriyle karşılaştırın.' };
    if (r.last_ok === false) return { cls: 'warning', head: 'Son ölçüm başarısız', text: words.fail, note: r.last_error };
    if (words.slow != null && p95 != null && p95 > words.slow[0]) return { cls: 'down', head: words.slow[1], text: words.slow[2] };
    if (avg != null && prev != null && avg > 2 * prev && avg - prev > words.trend) return { cls: 'warning', head: 'Son dönemde yavaşlıyor', text: 'Ortalama gecikme önceki döneme göre belirgin arttı; yük artışı, yeni bir ağ değişikliği veya hatta doygunluk olabilir. Artışın başladığı zamanı değişiklik kayıtlarıyla karşılaştırın.' };
    if (avg != null && p95 != null && p95 > 4 * avg && p95 > words.jit) return { cls: 'warning', head: 'Dalgalı gecikme', text: 'Çoğu ölçüm hızlı, ancak ara sıra çok yavaş cevaplar var; kuyruklanma, kısa süreli yoğunluk veya paket yeniden iletimi göstergesidir. Ortalamaya değil en yavaş dilime (p95) bakın.' };
    if (words.warn != null && ((p95 != null && p95 > words.warn[0]) || (avg != null && avg > words.warn[1]))) return { cls: 'warning', head: 'Gecikme yüksek', text: words.warn[2] };
    return { cls: 'up', head: 'Sağlıklı', text: words.ok };
  }

  const DNS_WORDS = {
    down: ['Cevap vermiyor', 'Sorgular neredeyse hiç cevaplanmıyor: DNS sunucusuna ulaşımı (UDP/TCP 53), servisin çalışıp çalışmadığını ve güvenlik duvarı kurallarını kontrol edin.'],
    fail: 'Genel başarı yüksek ama son sorgu başarısız oldu; sürüyorsa kayıt silinmiş, bölge/yetki sorunu veya sunucuda geçici bir hata olabilir.',
    slow: [150, 'Cevap süresi yüksek', 'İstemciler çözümleme için gözle görülür bekliyor; sunucu yük altında, yinelemeli sorgu üst sunucuya takılıyor veya ağ yolu yavaş olabilir. Üst DNS sunucularını ve sunucu CPU/bellek durumunu inceleyin.'],
    warn: [60, 40, 'Cevap süresi olması gerekenden uzun; iç DNS için genelde birkaç ms beklenir. Önbellek isabet oranını ve üst sunucu gecikmesini kontrol edin.'],
    ok: 'Sorgular hızlı ve tutarlı cevaplanıyor.', trend: 20, jit: 30,
  };
  function dnsInsight(r, peer) {
    const base = latencyInsight(r, DNS_WORDS);
    if (base.cls === 'up' && peer && peer.mismatch) return { cls: 'warning', head: 'Sunucular farklı cevap veriyor', text: 'Aynı ad için sunucuların döndürdüğü kayıtlar farklı: bölge aktarımı gecikmiş veya kayıtlar eşitlenmemiş olabilir (yük dağıtımlı DNS kullanıyorsanız bu beklenen olabilir).' };
    return base;
  }
  // Aynı ad + kayıt türü için birden çok DNS sunucusu izleniyorsa cevaplar karşılaştırılır.
  function dnsPeers(rows) {
    const g = {};
    for (const r of rows) {
      if (r.last_ok === false || !r.last_run) continue;
      const d = (r.detail || []).find((x) => x.k === 'Cevaplar');
      const ans = d ? String(d.v || '').split(', ').sort().join(',') : null;
      (g[r.target + '|' + r.record] = g[r.target + '|' + r.record] || []).push({ r, ans });
    }
    const out = new Map();
    for (const list of Object.values(g)) {
      const known = list.filter((x) => x.ans != null);
      const mismatch = known.length > 1 && new Set(known.map((x) => x.ans)).size > 1;
      for (const x of list) out.set(x.r.probe_id, { mismatch });
    }
    return out;
  }

  const NET_WORDS = {
    icmp: { down: ['Ping cevabı yok', 'Cihaz kapalı, ağ yolu kesik veya ICMP engelli olabilir. Aynı ağdaki diğer cihazlar da yanıt vermiyorsa sorun ara ağ cihazındadır.'], fail: 'Genel erişim iyi ama son ping cevapsız kaldı; sürüyorsa aralıklı kesinti veya paket kaybı vardır.', warn: [150, 150, 'Gecikme yüksek: ağ yolu uzun, hat doygun veya cihaz yoğun olabilir. Yol izleme sekmesiyle hangi sıçramada arttığına bakın.'], ok: 'Cihaz hızlı ve kararlı cevap veriyor.', trend: 20, jit: 50 },
    tcp: { down: ['Bağlantı kurulamıyor', 'Port bağlantı kabul etmiyor: servis durmuş, port kapalı veya güvenlik duvarı engelliyor olabilir.'], fail: 'Genel erişim iyi ama son bağlantı başarısız; servis yeniden başlıyor veya bağlantı limiti doluyor olabilir.', warn: [300, 200, 'Bağlantı kurulumu yavaş: ağ yolu, yük dengeleyici veya servisin kabul kuyruğu gecikiyor olabilir.'], ok: 'Servis bağlantıyı hızlı kabul ediyor.', trend: 30, jit: 80 },
    udp: { down: ['Cevap alınamıyor', 'UDP servisi cevap vermiyor veya cevap yolda engelleniyor olabilir; UDP bağlantısız olduğu için ağ kaybı da aynı belirtiyi verir.'], fail: 'Genel erişim iyi ama son istek cevapsız kaldı; UDP kaybı veya servis geçici sorunu olabilir.', warn: [300, 200, 'Cevap süresi yüksek: servis yoğun veya ağ yolu yavaş olabilir.'], ok: 'Servis hızlı cevap veriyor.', trend: 30, jit: 80 },
  };
  function netInsight(r) {
    if (r.type === 'tls') {
      const days = (r.last || {})['probe.cert_days_left'];
      if (!r.last_run) return NONE_INS;
      if (r.last_ok === false && days == null) return { cls: 'down', head: 'Bağlantı kurulamadı', text: 'TLS el sıkışması tamamlanamadı: servis kapalı, port engelli veya sunucu sertifika/protokol uyuşmazlığı olabilir.', note: r.last_error };
      if (days != null && days <= 0) return { cls: 'down', head: 'Sertifikanın süresi dolmuş', text: 'İstemciler bu servise güvenli bağlanamaz; sertifikayı hemen yenileyin.' };
      if (days != null && days <= 7) return { cls: 'down', head: 'Sertifika süresi dolmak üzere', text: 'Bir haftadan az kaldı; yenileme otomasyonunu ve sertifika zincirini kontrol edip hemen yenileyin.' };
      if (days != null && days <= 30) return { cls: 'warning', head: 'Yenileme zamanı yaklaşıyor', text: 'Süresi bir aydan kısa; yenilemeyi planlayın, otomatik yenileme varsa çalışıp çalışmadığını doğrulayın.' };
      const st = r.stats || {};
      if (st.avg_ms != null && st.avg_ms > 500) return { cls: 'warning', head: 'El sıkışma yavaş', text: 'TLS kurulumu uzun sürüyor; uzun sertifika zinciri, OCSP denetimi veya sunucu CPU baskısı olabilir.' };
      return { cls: 'up', head: 'Sağlıklı', text: 'Sertifika geçerli ve el sıkışma hızlı.' };
    }
    if (r.type === 'ntp') {
      const off = (r.last || {})['probe.ntp_offset_ms'];
      if (!r.last_run) return NONE_INS;
      if (r.last_ok === false && off == null) return { cls: 'down', head: 'NTP cevap vermiyor', text: 'Saat sunucusuna ulaşılamıyor (UDP 123); sunucu, güvenlik duvarı veya ağ yolunu kontrol edin.', note: r.last_error };
      if (off != null && off > 500) return { cls: 'down', head: 'Saat sapması yüksek', text: 'Saat yarım saniyeden fazla sapmış; Kerberos, TLS ve günlük korelasyonu bozulabilir. İstemcilerin bu sunucuyla eşitlendiğini doğrulayın.' };
      if (off != null && off > 100) return { cls: 'warning', head: 'Saat sapıyor', text: 'Sapma 100 ms üzerinde; eşitleme aralığını ve sunucunun kendi kaynağına (stratum) bakın.' };
      return { cls: 'up', head: 'Saat senkron', text: 'Sapma düşük, saat sunucusu güvenilir.' };
    }
    return latencyInsight(r, NET_WORDS[r.type] || NET_WORDS.tcp);
  }

  const insCell = (ins) => h('td', { class: 'small timing-note' }, pill(ins.cls, ins.head), h('div', null, ins.text), ins.note ? h('div', { class: 'muted' }, ins.note) : null);
  const upCell = (st) => h('td', { class: 'mono' }, st && st.up_pct != null ? '%' + st.up_pct.toFixed(st.up_pct >= 99.95 ? 0 : 1) : '—');
  const lastCell = (r) => h('td', { class: 'small' }, r.last_run ? [pill(r.last_ok === false ? 'down' : 'up', r.last_ok === false ? 'Hata' : 'Başarılı'), h('div', { class: 'muted' }, ago(r.last_run))] : '—');
  function netWin(rerender) {
    const sel = select(null, NET_WINS, PERF.nwin || '24h');
    sel.addEventListener('change', () => { PERF.nwin = sel.value; render(); });
    return sel;
  }

  async function perfDNS() {
    const d = await api('/api/perf/network?window=' + (PERF.nwin || '24h'));
    const rows = d.probes.filter((p) => p.type === 'dns');
    const peers = dnsPeers(rows);
    const trs = rows.map((r) => h('tr', null,
      h('td', null, r.asset, h('div', { class: 'muted small mono' }, r.server || 'sistem çözümleyici')),
      h('td', { class: 'mono small' }, r.target, h('div', { class: 'muted' }, r.record)), lastCell(r),
      h('td', { class: 'mono' }, pms((r.stats || {}).avg_ms)), h('td', { class: 'mono' }, pms((r.stats || {}).p95_ms)), upCell(r.stats),
      insCell(dnsInsight(r, peers.get(r.probe_id)))));
    return h('div', { class: 'stack' }, h('div', { class: 'row' }, netWin()),
      h('p', { class: 'muted small' }, 'DNS probe\'larının cevap süresi ve doğruluğu. Kurumsal DNS sunucularını ayrı probe olarak (sunucu adresi ve sorgulanacak ad ile) ekleyin; aynı ad birden çok sunucuda izleniyorsa cevaplar karşılaştırılır. İstatistikler seçili döneme aittir; yorum eşikleri: ortalama 40 ms, p95 60 ms üzeri uyarı, p95 150 ms üzeri yavaş.'),
      table(['DNS sunucusu', 'Sorgu', 'Son sorgu', 'Ort.', 'p95', 'Başarı', 'Yorum'], trs, 'DNS probe\'u yok. Probe\'lar sayfasından "DNS sorgusu" türünde, DNS sunucusu alanı dolu bir probe ekleyin.', PAGE),
      rows.some((r) => r.shared) ? h('p', { class: 'muted small' }, 'Bir asset\'te birden çok probe varsa süre ve başarı istatistikleri asset geneline aittir; sunucuları ayrı asset olarak tanımlamak daha doğru sonuç verir.') : null);
  }

  const NET_TYPE = { tls: 'TLS', ntp: 'NTP', tcp: 'TCP', icmp: 'ICMP', udp: 'UDP' };
  async function perfNet() {
    const d = await api('/api/perf/network?window=' + (PERF.nwin || '24h'));
    const rows = d.probes.filter((p) => NET_TYPE[p.type]);
    const trs = rows.map((r) => {
      const ex = r.type === 'tls' ? (((r.last || {})['probe.cert_days_left']) != null ? r.last['probe.cert_days_left'].toFixed(0) + ' gün kaldı' : '—')
        : r.type === 'ntp' ? (((r.last || {})['probe.ntp_offset_ms']) != null ? 'Sapma ' + r.last['probe.ntp_offset_ms'].toFixed(1) + ' ms' : '—') : '';
      return h('tr', null, h('td', null, r.asset), h('td', null, NET_TYPE[r.type]), h('td', { class: 'mono small' }, r.target), lastCell(r),
        h('td', { class: 'mono' }, pms((r.stats || {}).avg_ms)), h('td', { class: 'mono' }, pms((r.stats || {}).p95_ms)), upCell(r.stats), h('td', { class: 'small' }, ex),
        insCell(netInsight(r)));
    });
    return h('div', { class: 'stack' }, h('div', { class: 'row' }, netWin()),
      h('p', { class: 'muted small' }, 'TLS (sertifika), NTP (saat), TCP, ICMP ve UDP probe\'ları; her biri için seçili dönemin ortalama ve p95 cevap süresi, başarı oranı ve yorumu. Yol izleme probe\'ları "Yol izleme" sekmesindedir.'),
      table(['Asset', 'Tür', 'Hedef', 'Son ölçüm', 'Ort.', 'p95', 'Başarı', 'Ek ölçüm', 'Yorum'], trs, 'Bu türlerde probe yok.', PAGE));
  }

  // Yol izleme yorumu: işaret (DSCP) korunumu, belirgin gecikme artışı ve hedefte kayıp.
  function traceVerdict(r) {
    const out = [], hops = r.hops || [];
    if (!r.reached) out.push({ cls: 'warning', head: 'Hedefe ulaşılamadı', text: 'Yol hedeften önce kesildi; güvenlik duvarı ICMP\'yi engelliyor veya hedef cevap vermiyor olabilir. Son cevap veren sıçrama sorunun başladığı bölgeyi gösterir.' });
    if (r.dscp > 0) {
      if (!r.mark_seen) out.push({ cls: 'unknown', head: 'İşaret doğrulanamadı', text: 'Yönlendiriciler paket başlığını geri alıntılamadı; DSCP işaretinin yolda korunup korunmadığı görülemiyor.' });
      else if (r.mark_changed_ttl > 0) out.push({ cls: 'warning', head: 'İşaret yolda değişti', text: r.mark_changed_ttl + '. sıçramada DSCP işareti değişti. Bu noktada bir QoS politikası işareti yeniden yazıyor veya siliyor olabilir; öncelikli trafik buradan sonra işaretsiz ilerler.' });
      else out.push({ cls: 'up', head: 'İşaret korundu', text: 'DSCP işareti okunabilen sıçramalarda değişmeden ilerledi.' });
    } else if (r.mark_changed_ttl > 0) out.push({ cls: 'unknown', head: 'Ağ işaret ekliyor', text: 'İşaretsiz gönderilen paket ' + r.mark_changed_ttl + '. sıçramada işaretli görüldü; ağ trafiği kendisi sınıflandırıp işaretliyor.' });
    const live = hops.filter((x) => x.ip && x.rtt_ms != null);
    const e2e = live.length ? live[live.length - 1].rtt_ms : 0;
    let big = null, prev = 0;
    for (const x of live) { const dlt = x.rtt_ms - prev; if (!big || dlt > big.d) big = { d: dlt, ttl: x.ttl }; prev = x.rtt_ms; }
    if (big && big.d >= 20 && big.d >= 0.5 * e2e && live.length > 2) out.push({ cls: 'warning', head: 'Belirgin gecikme artışı', text: big.ttl + '. sıçramada gecikme belirgin artıyor (cihaz ya da sonraki bağlantı). Yönlendiriciler ICMP\'ye düşük öncelik verdiği için ara sıçramalara tek başına güvenmeyin; uçtan uca süre esastır.' });
    const lastHop = hops[hops.length - 1];
    if (r.reached && lastHop && lastHop.loss_pct > 0) out.push({ cls: 'warning', head: 'Hedefte paket kaybı', text: 'Hedef sıçramada gönderilen paketlerin bir kısmı cevapsız kaldı; hat kalitesi veya hedefin ICMP sınırlaması olabilir. Tekrarlayan ölçümle doğrulayın.' });
    if (!out.length) out.push({ cls: 'up', head: 'Sağlıklı', text: 'Hedefe ulaşıldı, yolda belirgin gecikme artışı veya kayıp görülmedi.' });
    return { list: out, e2e };
  }

  function renderTrace(r) {
    const v = traceVerdict(r);
    let prev = 0;
    const rows = r.hops.map((x) => {
      const delta = x.ip ? Math.max(0, x.rtt_ms - prev) : null;
      if (x.ip) prev = x.rtt_ms;
      const changed = r.mark_changed_ttl && x.ttl === r.mark_changed_ttl;
      return h('tr', { class: changed ? 'hop-flag' : null },
        h('td', { class: 'mono' }, String(x.ttl)), h('td', { class: 'mono' }, x.ip || '*'),
        h('td', { class: 'small' }, x.name || '', x.asset ? h('div', null, pill('up', x.asset)) : null),
        h('td', { class: 'mono' }, x.ip ? x.rtt_ms.toFixed(1) + ' ms' : '—'), h('td', { class: 'mono' }, delta == null ? '—' : '+' + delta.toFixed(1) + ' ms'),
        h('td', { class: 'mono' }, x.ip ? x.jitter_ms.toFixed(1) + ' ms' : '—'),
        h('td', { class: x.loss_pct > 0 ? 'bad' : '' }, '%' + Math.round(x.loss_pct)),
        h('td', { class: 'mono' }, x.dscp_in == null ? '—' : dscpName(x.dscp_in)));
    });
    return h('div', { class: 'stack' },
      h('div', { class: 'panel' }, h('div', { class: 'row' },
        h('span', null, 'Hedef: ', h('b', { class: 'mono' }, r.dest_ip)), h('span', null, 'Sıçrama: ', h('b', null, String(r.hops.length))),
        h('span', null, 'Uçtan uca (gidiş-dönüş): ', h('b', { class: 'mono' }, v.e2e ? v.e2e.toFixed(1) + ' ms' : '—')),
        h('span', null, 'Tek yön (yaklaşık): ', h('b', { class: 'mono' }, v.e2e ? (v.e2e / 2).toFixed(1) + ' ms' : '—')),
        h('span', null, 'Gönderilen işaret: ', h('b', null, dscpName(r.dscp)))),
        h('div', { class: 'muted small' }, 'Tek yön süresi ölçülemez (uçlarda saat eşitliği gerekir); gidiş-dönüşün yarısı yaklaşık bir değerdir ve gidiş-dönüş yolları farklıysa yanıltabilir.')),
      h('div', { class: 'stack' }, v.list.map((x) => h('div', { class: 'panel' }, pill(x.cls, x.head), h('div', { class: 'small' }, x.text)))),
      table(['#', 'Adres', 'Ad / asset', 'Gecikme', 'Artış', 'Dalgalanma', 'Kayıp', 'DSCP (varışta)'], rows, 'Sonuç yok.', PAGE),
      h('p', { class: 'muted small' }, 'Her sıçramada paketin ulaştığı cihaz ve gidiş-dönüş süresi gösterilir. "DSCP (varışta)", yönlendiricinin geri döndürdüğü başlıktan okunan işarettir. Ara cihazlar ICMP cevaplarına düşük öncelik verebilir; bu yüzden ara sıçramalardaki süre ve kayıp yanıltıcı olabilir, güvenilir olan uçtan uca süredir.'));
  }

  function traceProbeInsight(r) {
    const l = r.last || {};
    if (!r.last_run) return NONE_INS;
    if (r.last_ok === false) return { cls: 'down', head: 'Hedefe ulaşılamıyor', text: 'Yol hedefe kadar tamamlanamadı; son cevap veren sıçramaya bakarak sorunun başladığı bölgeyi daraltın.', note: r.last_error };
    if (l['net.path.changed'] === 1) return { cls: 'warning', head: 'Rota değişti', text: 'Paketler önceki ölçümden farklı cihazlar üzerinden gidiyor; yedek hatta geçiş, yönlendirme değişikliği veya arıza olabilir. Gecikmenin de değişip değişmediğine bakın.' };
    if (r.dscp > 0 && l['net.path.dscp_changed_ttl'] > 0) return { cls: 'warning', head: 'İşaret yolda değişti', text: l['net.path.dscp_changed_ttl'] + '. sıçramada DSCP işareti değişiyor; bu cihazda QoS politikasını kontrol edin.' };
    if (l['net.path.loss_pct'] > 0) return { cls: 'warning', head: 'Hedefte paket kaybı', text: 'Son ölçümde hedefe giden paketlerin bir kısmı cevapsız kaldı; tekrarlıyorsa hat kalitesini inceleyin.' };
    return { cls: 'up', head: 'Sağlıklı', text: 'Hedefe ulaşılıyor, rota kararlı.' };
  }

  async function perfPath() {
    const d = await api('/api/perf/network?window=' + (PERF.nwin || '24h'));
    const rows = d.probes.filter((p) => p.type === 'trace');
    const tgt = h('input', { placeholder: '10.0.0.1 veya ornek.com', maxlength: '253', required: true });
    const dsel = select(null, DSCP_CHOICES.map(([k, l]) => [String(k), l]), String(PERF.dscp || 0));
    const btn = h('button', { class: 'btn', type: 'submit' }, 'Yolu izle');
    const msg = h('p', { class: 'msg', role: 'status' });
    const out = h('div');
    const form = h('form', { class: 'inline' }, field('Hedef (B noktası)', tgt), field('Paket işareti (DSCP)', dsel), btn);
    form.addEventListener('submit', async (e) => {
      e.preventDefault(); PERF.dscp = Number(dsel.value);
      msg.className = 'msg'; msg.textContent = 'İzleniyor… (en fazla 25 sn)'; btn.disabled = true; out.replaceChildren();
      try { const r = await api('/api/trace', { method: 'POST', body: { target: tgt.value.trim(), dscp: Number(dsel.value) } }); msg.textContent = ''; out.replaceChildren(renderTrace(r)); }
      catch (err) { msg.className = 'msg error'; msg.textContent = err.message; }
      btn.disabled = false;
    });
    const trs = rows.map((r) => h('tr', null, h('td', null, r.asset), h('td', { class: 'mono small' }, r.target), h('td', { class: 'mono' }, r.dscp ? dscpName(r.dscp) : '—'),
      h('td', { class: 'mono' }, (r.last || {})['net.path.hops'] != null ? String(r.last['net.path.hops']) : '—'),
      h('td', { class: 'mono' }, pms((r.last || {})['net.path.rtt_ms'])), h('td', { class: 'mono' }, (r.last || {})['net.path.loss_pct'] != null ? '%' + Math.round(r.last['net.path.loss_pct']) : '—'),
      insCell(traceProbeInsight(r)),
      h('td', null, r.detail && r.detail.length ? h('details', null, h('summary', { class: 'small' }, 'Sıçramalar'), h('div', { class: 'mono small' }, r.detail.filter((x) => /^Sıçrama|^Gönderilen/.test(x.k || '')).map((x) => h('div', null, x.k + ': ' + x.v)))) : '')));
    return h('div', { class: 'stack' },
      h('p', { class: 'muted small' }, 'Bir noktadan (APP_MON sunucusu) hedefe giden yolu izler: paketin geçtiği cihazlar, her sıçramadaki gecikme ve isteğe bağlı olarak pakete yazılan DSCP işaretinin yolda korunup korunmadığı. Ham ICMP yetkisi gerekir (Linux sunucu).'),
      h('h3', null, 'Anlık izleme'), form, msg, out,
      h('h3', null, 'Sürekli izlenen yollar'),
      h('div', { class: 'row' }, netWin()),
      table(['Asset', 'Hedef', 'DSCP', 'Sıçrama', 'Uçtan uca', 'Kayıp', 'Yorum', 'Ayrıntı'], trs, 'Yol izleme probe\'u yok. Probe\'lar sayfasından "Yol izleme" türünde probe ekleyin (isteğe bağlı DSCP işareti seçilebilir).', PAGE));
  }

  // Bağlantı yorumları: TCP RTT (ağ gecikmesi) ve haritadaki durum.
  function connInsight(l) {
    if (l.rtt_ms != null && l.rtt_ms >= 300) return { cls: 'down', head: 'Ağ gecikmesi çok yüksek', text: 'Bu iki makine arasındaki TCP gidiş-dönüş süresi çok uzun; uzak bölge, tıkalı hat veya yol üzerindeki cihaz kuyruğu olabilir. Yol izleme sekmesinden hop hop bakın.' };
    if (l.rtt_ms != null && l.rtt_ms >= 100) return { cls: 'warning', head: 'Ağ gecikmesi yüksek', text: 'Aynı veri merkezi için beklenenden uzun; makineler farklı bölgedeyse normal olabilir.' };
    if (!l.edge_exists) return { cls: 'warning', head: 'Haritada yok', text: 'Bu bağımlılık gerçekten var ama topolojide tanımlı değil; eklerseniz bir arıza durumunda etki analizi bu bağı da hesaba katar.' };
    return { cls: 'up', head: 'Sağlıklı', text: 'Bağlantı gecikmesi düşük ve bağımlılık topolojide tanımlı.' };
  }
  const KNOWN_PORTS = { 22: 'SSH', 25: 'SMTP', 53: 'DNS', 80: 'HTTP', 443: 'HTTPS', 389: 'LDAP', 636: 'LDAPS', 1433: 'SQL Server', 1521: 'Oracle', 3306: 'MySQL', 5432: 'PostgreSQL', 5672: 'AMQP', 6379: 'Redis', 9092: 'Kafka', 9200: 'Elasticsearch', 27017: 'MongoDB' };
  function unknownConnInsight(u) {
    const svc = KNOWN_PORTS[u.port];
    if (u.rtt_ms != null && u.rtt_ms >= 300) return { cls: 'warning', head: 'Uzak ve yavaş hedef', text: (svc ? svc + ' hizmeti; ' : '') + 'bu dış hedefe giden bağlantı çok yavaş. Kritik bir bağımlılıksa bir asset olarak tanımlayıp izleyin.' };
    if (svc) return { cls: 'unknown', head: svc + ' hedefi', text: 'APM_MON\'da tanımlı olmayan bir ' + svc + ' hedefi. Bir iş bağımlılığıysa asset olarak ekleyin; dış servisse yok sayabilirsiniz.' };
    return { cls: 'unknown', head: 'Tanınmayan hedef', text: 'Bu adres/port APM_MON\'da tanımlı bir varlıkla eşleşmiyor. Bir iç servis olabilir; tanımlarsanız haritaya otomatik girer.' };
  }
  async function perfConns() {
    const d = await api('/api/perf/conns');
    const msg = h('p', { class: 'msg', role: 'status' });
    const links = d.links.map((l) => {
      const add = h('button', { type: 'button', class: 'small primary' }, 'Topolojiye ekle');
      add.addEventListener('click', async () => {
        add.disabled = true;
        try { await api('/api/topology/edges', { method: 'POST', body: { source: l.source_id, target: l.target_id, kind: 'depends_on' } }); render(); }
        catch (e) { add.disabled = false; msg.className = 'msg error'; msg.textContent = e.message; }
      });
      return h('tr', null, h('td', null, l.source, ' → ', l.target), h('td', { class: 'mono' }, String(l.port)), h('td', { class: 'mono' }, String(l.conns)),
        h('td', { class: 'mono' }, l.rtt_ms == null ? '—' : pms(l.rtt_ms)), insightCell(connInsight(l)), h('td', null, l.edge_exists ? pill('up', 'Topolojide') : canWrite() ? add : pill('warning', 'Önerilen')));
    });
    const unk = d.unknown.map((u) => h('tr', null, h('td', null, u.asset), h('td', { class: 'mono' }, u.peer), h('td', { class: 'mono' }, String(u.port)), h('td', { class: 'mono' }, String(u.conns)), h('td', { class: 'mono' }, u.rtt_ms == null ? '—' : pms(u.rtt_ms)), insightCell(unknownConnInsight(u))));
    return h('div', { class: 'stack' },
      h('p', { class: 'muted small' }, 'SSH ile izlenen Linux makinelerde (Probe\'lar › Sunucu kaynakları › yöntem SSH) kurulu TCP bağlantıları okunur: kim kime hangi portla bağlı, TCP RTT kaç ms. Karşı taraf APP_MON\'da tanımlı bir makineyle eşleşirse bağımlılık önerilir. Gecikme uygulama değil ağ (TCP RTT) gecikmesidir.'), msg,
      h('h3', null, 'Önerilen bağımlılıklar'),
      table(['Bağlantı', 'Port', 'Bağlantı sayısı', 'TCP RTT', 'Yorum', 'Topoloji'], links, 'Henüz eşleşen bağlantı yok. Ajan kuruluysa Ajanlar sayfasında "Bağlantı tabanlı servis haritası" (ve Uygulama keşfi) veri türleri açık olmalıdır; karşı makineler de ajanla veya SSH probe\'uyla izleniyorsa veya probe hedefi olarak tanımlıysa burada görünür.', PAGE),
      h('h3', null, 'Tanınmayan hedefler'),
      table(['Makine', 'Karşı adres', 'Port', 'Bağlantı sayısı', 'TCP RTT', 'Yorum'], unk, 'Tanınmayan dış bağlantı yok.', PAGE),
      h('p', { class: 'muted small' }, `${d.hosts.length} makineden veri geliyor. Loopback ve makinenin kendi adresleri atlanır. Öneriler otomatik uygulanmaz; yalnızca "Topolojiye ekle" ile siz onaylarsınız.`));
  }

  const FLOW_WINS = [['1h', 'Son 1 saat'], ['6h', 'Son 6 saat'], ['24h', 'Son 24 saat'], ['7d', 'Son 7 gün']];
  const fmtBytes = (b) => (b == null ? '—' : b < 1024 ? b + ' B' : b < 1048576 ? (b / 1024).toFixed(1) + ' KB' : b < 1073741824 ? (b / 1048576).toFixed(1) + ' MB' : (b / 1073741824).toFixed(2) + ' GB');
  // Ağ akışı yorumları: en çok gönderen payı, baskın port ve tanımsız adreslerle konuşma oranı.
  function flowInsights(r) {
    const out = [], total = r.total_bytes || 0;
    if (!total) return out;
    const t = (r.talkers || [])[0];
    if (t) {
      const sh = (t.bytes / total) * 100;
      out.push(sh >= 60 ? { cls: 'warning', head: 'Trafik tek makinede toplanıyor', text: `${t.label} toplam trafiğin %${sh.toFixed(0)}'ini gönderiyor; yedekleme/replikasyon gibi planlı bir iş değilse incelemeye değer.` }
        : { cls: 'up', head: 'Dağılım dengeli', text: `En çok gönderen ${t.label} (%${sh.toFixed(0)}); tek bir kaynakta yoğunlaşma yok.` });
    }
    const p = (r.ports || [])[0];
    if (p) {
      const svc = KNOWN_PORTS[p.port], sh = (p.bytes / total) * 100;
      out.push({ cls: 'unknown', head: `En yoğun port ${p.proto}/${p.port}`, text: `${svc ? svc + ' · ' : ''}trafiğin %${sh.toFixed(0)}'i bu porta gidiyor.` });
    }
    const conv = r.conversations || [];
    const unk = conv.filter((x) => !x.src_asset && !x.dst_asset).reduce((a, x) => a + x.bytes, 0);
    const convTotal = conv.reduce((a, x) => a + x.bytes, 0);
    if (convTotal && unk / convTotal >= 0.3) out.push({ cls: 'warning', head: 'Tanımsız adresler konuşuyor', text: `En çok konuşan çiftlerin %${((unk / convTotal) * 100).toFixed(0)}'inde iki taraf da APM_MON'da tanımlı değil; sık konuşan adresleri asset olarak ekleyin.` });
    return out;
  }
  async function perfFlows() {
    const since = select(null, FLOW_WINS, PERF.fwin || '1h');
    since.addEventListener('change', () => { PERF.fwin = since.value; render(); });
    const r = await api('/api/flows/summary?since=' + (PERF.fwin || '1h') + (PERF.asset ? '&asset=' + PERF.asset : ''));
    if (!r.enabled) {
      return h('div', { class: 'stack' }, h('div', { class: 'panel' }, h('b', null, 'NetFlow/IPFIX toplayıcı kapalı. '),
        'Router veya güvenlik duvarınız NetFlow (v5/v9) ya da IPFIX gönderebiliyorsa sunucuda ', h('code', null, 'APM_NETFLOW_ADDR=:2055'), ' (UDP) ve mümkünse ', h('code', null, 'APM_NETFLOW_ALLOW=<ihracatçı IP\'leri>'),
        ' verip yeniden başlatın; cihazda flow export hedefini bu sunucunun IP\'si ve portu yapın. Docker\'da UDP portunu ayrıca yayınlamanız gerekir. Ayrıntı için Kullanım Kılavuzu › Ağ akışı.'));
    }
    const c = r.collector || {};
    const bars = (list, cls) => list.length ? rankBars(list.map((x) => ({ kind: cls, label: x.label, value: x.bytes, text: fmtBytes(x.bytes) })), '') : h('p', { class: 'muted' }, 'Veri yok.');
    const conv = r.conversations.map((x) => h('tr', null, h('td', null, h('b', null, x.src_asset || x.src), x.src_asset ? h('div', { class: 'muted small mono' }, x.src) : null),
      h('td', null, h('b', null, x.dst_asset || x.dst), x.dst_asset ? h('div', { class: 'muted small mono' }, x.dst) : null), h('td', { class: 'mono' }, `${x.proto}/${x.port}`), h('td', { class: 'mono' }, fmtBytes(x.bytes)), h('td', { class: 'mono' }, String(x.packets))));
    const flowNotes = flowInsights(r);
    return h('div', { class: 'stack' }, h('div', { class: 'filters' }, since),
      h('div', { class: 'kpis' }, kpi('Toplam trafik', fmtBytes(r.total_bytes), 'seçili dönem', null, ''), kpi('Akış kaydı', String(r.flows), 'özetlenen', null, ''),
        kpi('İhracatçı', String(c.exporters || 0), c.last ? 'son paket ' + ago(new Date(c.last * 1000).toISOString()) : 'paket yok', null, c.last ? '' : 'warn'),
        kpi('Çözülemeyen', String((c.invalid || 0) + (c.missing_template || 0)), c.missing_template ? 'şablon bekleniyor' : 'bozuk paket', null, c.invalid ? 'warn' : '')),
      flowNotes.length ? h('div', { class: 'panel' }, h('h3', null, 'Yorum'), ...flowNotes.map((n) => h('div', { class: 'timing-note' }, pill(n.cls, n.head), ' ', n.text))) : null,
      h('div', { class: 'rcols' }, h('div', { class: 'stack' }, h('h3', null, 'En çok gönderenler'), bars(r.talkers, 'host')), h('div', { class: 'stack' }, h('h3', null, 'En çok alanlar'), bars(r.receivers, 'host'))),
      h('div', { class: 'rcols' }, h('div', { class: 'stack' }, h('h3', null, 'Protokoller'), bars(r.protocols, 'network')),
        h('div', { class: 'stack' }, h('h3', null, 'Hedef portlar'), bars(r.ports.map((x) => ({ label: `${x.proto}/${x.port}`, bytes: x.bytes })), 'service'))),
      h('h3', null, 'En çok konuşan çiftler'), table(['Kaynak', 'Hedef', 'Protokol/port', 'Trafik', 'Paket'], conv, 'Bu dönemde akış yok.'),
      h('p', { class: 'muted small' }, `Ham akış saklanmaz: ${Math.round((c.bucket_sec || 300) / 60)} dk'lık özetlerde ihracatçı başına en çok ${c.top_n || 500} konuşma tutulur, ${c.retention_days || 7} gün saklanır. Örnekleme bildiren cihazlarda değerler örnekleme oranıyla çarpılır. Adresi tanımlı bir asset'e ait olanlar adıyla görünür; bu asset'ler için flow.rx_bps / flow.tx_bps metrikleri alarm kuralında kullanılabilir.`));
  }
  async function viewPerformans() {
    await refreshHeader();
    const tabs = h('div', { class: 'seg', role: 'group', 'aria-label': 'Performans sekmeleri' }, PERF_TABS.map(([id, label]) => {
      const b = h('button', { type: 'button', 'aria-pressed': String(PERF.tab === id) }, label);
      b.addEventListener('click', () => { PERF.tab = id; PERF.asset = null; render(); });
      return b;
    }));
    const body = await ({ apps: perfApps, queries: perfQueries, timing: perfTiming, dns: perfDNS, net: perfNet, path: perfPath, conns: perfConns, flows: perfFlows }[PERF.tab] || perfApps)();
    return h('section', { class: 'stack' }, h('h2', null, 'Performans'),
      h('p', { class: 'muted small' }, 'Hedef sistemlere ajan veya kod değişikliği olmadan uygulama performansı: web sunucusu günlüklerinden uç nokta yanıt süreleri, veritabanı yavaş sorguları, HTTP aşama süreleri ve makineler arası bağlantılar.'),
      tabs, body);
  }

  // ---- Saldırı yüzeyi (v2.5.0) ----
  const SURF = { all: false, tab: 'drift', inv: null };
  const SURF_KIND = { port: 'Dinleyen port', endpoint: 'Uç nokta', tls: 'TLS sürümü', header: 'Güvenlik başlığı', banner: 'Sunucu bilgisi (banner)', scan: 'Tarama izi' };
  const SURF_CHANGE = { added: 'Yeni', removed: 'Kayboldu', burst: 'Patlama' };
  const SURF_HEADER = { 'strict-transport-security': 'HSTS', 'content-security-policy': 'CSP', 'x-frame-options': 'X-Frame-Options', 'x-content-type-options': 'X-Content-Type-Options', 'referrer-policy': 'Referrer-Policy' };

  async function yuzeyDrift() {
    const [ev, base] = await Promise.all([api('/api/surface/events?state=' + (SURF.all ? 'all' : 'open')), api('/api/surface/baseline')]);
    const msg = h('p', { class: 'msg', role: 'status' });
    const review = async (e) => {
      const note = window.prompt('Not (isteğe bağlı) — örn. "planlı değişiklik, CAB-123"', '');
      if (note === null) return;
      try { await api(`/api/surface/events/${e.id}/review`, { method: 'POST', body: { note } }); render(); } catch (er) { msg.className = 'msg error'; msg.textContent = er.message; }
    };
    const rows = ev.events.map((e) => h('tr', { class: e.reviewed ? 'off' : '' },
      h('td', null, pill(e.severity, SEV[e.severity])), h('td', null, e.asset), h('td', null, SURF_KIND[e.kind] || e.kind),
      h('td', { class: 'mono' }, SURF_HEADER[e.item] || e.item, e.detail ? h('div', { class: 'muted small' }, e.detail) : null),
      h('td', null, SURF_CHANGE[e.change] || e.change), h('td', { class: 'mono small' }, ago(e.at)),
      h('td', null, e.reviewed ? h('span', { class: 'small muted' }, 'Gözden geçirildi: ' + e.reviewed_by + (e.note ? ' — ' + e.note : '')) : canWrite() ? h('button', { type: 'button', class: 'small', onclick: () => review(e) }, 'Gözden geçirdim') : '')));
    const allBox = h('input', { type: 'checkbox' });
    allBox.checked = SURF.all;
    allBox.addEventListener('change', () => { SURF.all = allBox.checked; render(); });
    const resetBtn = (b) => h('button', { type: 'button', class: 'small', onclick: async () => {
      if (!window.confirm(`${b.asset} için referans sıfırlansın mı? Açık sapmalar kapatılır ve yüzey yeniden öğrenilir.`)) return;
      try { await api('/api/surface/reset', { method: 'POST', body: { asset_id: b.asset_id } }); render(); } catch (er) { msg.className = 'msg error'; msg.textContent = er.message; }
    } }, 'Referansı sıfırla');
    const brow = base.map((b) => h('tr', null, h('td', null, b.asset),
      h('td', { class: 'mono small' }, (b.ports || []).join(', ') || '—'),
      h('td', { class: 'mono small' }, (b.tlss || []).join(', ') || '—'),
      h('td', { class: 'small' }, (b.headers || []).map((x) => SURF_HEADER[x] || x).join(', ') || '—'),
      h('td', { class: 'mono' }, String(b.endpoints || 0)),
      h('td', null, canWrite() ? resetBtn(b) : '')));
    return h('div', { class: 'stack' },
      h('p', { class: 'muted small' }, 'Normal izleme verisinden bir referans öğrenilir; sonradan ortaya çıkan yeni dinleyen port, görülmemiş uç nokta, TLS düşürme, güvenlik başlığı kaybı ve 4xx patlaması (tarama izi) sapma olarak listelenir. Hiçbir tarama veya saldırı denemesi yapılmaz: yalnızca zaten toplanan veri kullanılır.'),
      h('div', { class: 'kpis' }, kpi('Gözden geçirilmemiş', String(ev.open), 'sapma', null, ev.open ? 'warn' : ''), kpi('Kritik', String(ev.critical), 'sapma', null, ev.critical ? 'bad' : '')),
      msg,
      h('div', { class: 'row' }, h('h3', null, 'Sapmalar'), h('label', { class: 'small surf-all' }, allBox, ' gözden geçirilenleri de göster')),
      table(['Önem', 'Asset', 'Tür', 'Öğe', 'Değişim', 'Zaman', ''], rows, 'Referanstan sapma yok.', PAGE),
      h('h3', null, 'Referans durum'),
      table(['Asset', 'Dinleyen portlar', 'TLS', 'Güvenlik başlıkları', 'Uç nokta sayısı', ''], brow,
        'Henüz referans yok. SSH ile izlenen makineler (portlar), HTTPS probe\'ları (TLS, başlıklar) ve erişim günlüğü (uç noktalar) geldikçe oluşur.', PAGE),
      h('p', { class: 'muted small' }, 'İlk gözlem referans olur (sapma sayılmaz). Uç noktalar yeni bir uygulama için 24 saat öğrenilir. Planlı bir değişiklikten sonra sapmayı "Gözden geçirdim" ile kapatın; referans zaten yeni durumu içerir. Alarm istiyorsanız Alarmlar › şablonlardan "Saldırı yüzeyi sapması" kuralını ekleyin (surface.drift > 0).'));
  }

  // ---- Envanter: dinleyen servisler, çalışma zamanı sürümleri (EOL) ve bağımlılık zafiyetleri ----
  const SEV_VULN = { critical: ['down', 'Kritik'], high: ['down', 'Yüksek'], medium: ['warning', 'Orta'], low: ['unknown', 'Düşük'], unknown: ['unknown', 'Belirsiz'] };
  const RISKY_PORTS = new Set([21, 23, 69, 135, 139, 445, 1433, 1521, 2375, 3306, 3389, 5432, 5900, 6379, 9200, 11211, 27017]);
  const RT_LABEL = { node: 'Node.js', java: 'Java', php: 'PHP', python: 'Python', ruby: 'Ruby', dotnet: '.NET', nginx: 'nginx', httpd: 'Apache httpd', postgresql: 'PostgreSQL', mysql: 'MySQL', mariadb: 'MariaDB', redis: 'Redis', mongodb: 'MongoDB', openssh: 'OpenSSH', go: 'Go' };
  const eolPill = (e) => e.status === 'eol' ? pill('down', 'Destek dışı' + (e.date ? ' · ' + e.date : '')) : e.status === 'soon' ? pill('warning', 'Destek bitiyor · ' + e.date) : e.status === 'ok' ? pill('up', 'Destekli · ' + e.date) : h('span', { class: 'muted small' }, '—');
  // Envanter yorumu: kritik zafiyet, destek dışı sürüm ve riskli açık port sırasıyla değerlendirilir.
  function invInsight(a, adv) {
    const risky = (a.listeners || []).filter((l) => l.scope === 'net' && RISKY_PORTS.has(l.port));
    if (a.critical) return { cls: 'down', head: 'Kritik zafiyet', text: `${a.critical} kritik zafiyet bildirilmiş bir bağımlılık kullanılıyor. Ayrıntıda düzeltme sürümünü görüp bağımlılığı güncelleyin.` };
    if (risky.length) return { cls: 'down', head: 'Riskli port açık', text: `${risky.map((l) => l.port + (KNOWN_PORTS[l.port] ? ' (' + KNOWN_PORTS[l.port] + ')' : '')).join(', ')} ağdan erişilebiliyor. Gerekmiyorsa yalnızca yerel adrese bağlayın ya da güvenlik duvarıyla kısıtlayın.` };
    if (a.eol_count) return { cls: 'warning', head: 'Destek dışı sürüm', text: `${a.eol_count} servis/çalışma zamanı üreticisinin güvenlik yamasını artık yayımlamıyor; bilinen açıklar kapatılmaz. Desteklenen bir sürüme geçmeyi planlayın.` };
    if (a.high) return { cls: 'warning', head: 'Yüksek önemde zafiyet', text: `${a.high} yüksek önemde zafiyet var; bağımlılık güncellemesi planlayın.` };
    if (!adv.records && a.deps_count) return { cls: 'unknown', head: 'Zafiyet kaydı yok', text: 'Bağımlılıklar toplandı ama zafiyet kayıt dizini boş; bu yüzden CVE eşleşmesi yapılamıyor. Üstteki bilgi kutusuna bakın.' };
    return { cls: 'up', head: 'Temiz', text: 'Bilinen zafiyet, destek dışı sürüm veya riskli açık port görünmüyor.' };
  }
  // Tek asset'in yazılım envanteri (portlar, destek durumu, zafiyetler, bağımlılıklar); Saldırı yüzeyi ve Asset kartı ortak kullanır.
  function invDetail(a, adv, msg) {
      const ins = invInsight(a, adv);
      const lrows = a.listeners.slice().sort((x, y) => (y.scope === 'net') - (x.scope === 'net') || x.port - y.port).map((l) => h('tr', null, h('td', { class: 'mono' }, String(l.port), KNOWN_PORTS[l.port] ? h('div', { class: 'muted small' }, KNOWN_PORTS[l.port]) : null),
        h('td', null, l.scope === 'net' ? pill(RISKY_PORTS.has(l.port) ? 'down' : 'warning', 'Ağdan erişilebilir') : pill('up', 'Yalnızca yerel')),
        h('td', null, h('b', null, l.proc), l.container ? h('span', { class: 'muted small' }, ' · kapsayıcı') : null, l.app && l.app !== l.proc ? h('div', { class: 'muted small' }, l.app) : null),
        h('td', { class: 'mono small' }, l.runtime ? (RT_LABEL[l.runtime] || l.runtime) + (l.version && l.version !== 'unknown' ? ' ' + l.version : '') : '—'), h('td', null, eolPill(l.eol))));
      const vrows = a.vulns.map((v) => { const [c, n] = SEV_VULN[v.severity] || SEV_VULN.unknown; return h('tr', null, h('td', null, pill(c, n)),
        h('td', null, h('b', null, v.name), h('span', { class: 'mono small' }, ' ' + v.version), h('div', { class: 'muted small' }, `${v.app}${v.src === 'jar' ? ' · sürüm jar adından, tahmini' : v.src === 'manifest' ? ' · bildirilen sürüm, tahmini' : ''}`)),
        h('td', { class: 'small' }, h('span', { class: 'mono' }, (v.aliases || []).find((x) => x.startsWith('CVE-')) || v.id), v.summary ? h('div', { class: 'muted' }, v.summary) : null),
        h('td', { class: 'mono small' }, v.fixed ? '≥ ' + v.fixed : '—')); });
      const depBox = h('div');
      const depBtn = h('button', { type: 'button', class: 'small', onclick: async () => {
        depBtn.disabled = true;
        try {
          const r = await api(`/api/inventory/${a.asset_id}/deps`);
          const drows = r.deps.map((x) => h('tr', null, h('td', null, x.app), h('td', { class: 'mono small' }, x.eco), h('td', { class: 'mono' }, x.name), h('td', { class: 'mono small' }, x.version), h('td', null, x.vulns.length ? pill('down', x.vulns.length + ' zafiyet') : '')));
          depBox.replaceChildren(table(['Uygulama', 'Tür', 'Paket', 'Sürüm', ''], drows, 'Bağımlılık yok.', PAGE)); depBtn.hidden = true;
        } catch (e) { depBtn.disabled = false; msg.className = 'msg error'; msg.textContent = e.message; }
      } }, `Tüm bağımlılıkları göster (${a.deps_count})`);
      return h('div', { class: 'stack' }, h('div', { class: 'panel timing-note' }, pill(ins.cls, ins.head), h('div', null, ins.text)),
        h('p', { class: 'muted small' }, 'Son envanter: ' + ago(a.updated_at) + '. Yalnızca yerel (127.0.0.1) dinleyen portlar dış saldırı yüzeyine girmez.'),
        h('h3', null, `Dinleyen servisler (${a.listeners.length})`),
        table(['Port', 'Erişim', 'Süreç', 'Çalışma zamanı', 'Destek durumu'], lrows, 'Dinleyen port bulunamadı.', PAGE),
        h('h3', null, `Zafiyetler (${a.vuln_total})`),
        a.vuln_total ? table(['Önem', 'Paket', 'Kayıt', 'Düzeltme'], vrows, '', PAGE) : h('p', { class: 'muted' }, adv.records ? 'Eşleşen bilinen zafiyet yok.' : 'Zafiyet kaydı yüklü olmadığı için eşleştirme yapılamadı.'),
        a.deps_count ? h('div', null, h('h3', null, `Bağımlılıklar (${a.deps_count})`), depBtn, depBox) : null);
  }
  async function yuzeyEnvanter() {
    const d = await api('/api/inventory');
    const adv = d.advisory || {}, t = d.totals || {};
    const msg = h('p', { class: 'msg', role: 'status' });
    const reload = ME.role === 'admin' && adv.enabled ? h('button', { type: 'button', class: 'small', onclick: async () => {
      try { await api('/api/inventory/advisories/reload', { method: 'POST' }); msg.className = 'msg ok'; msg.textContent = 'Zafiyet kayıtları yeniden yükleniyor; birkaç dakika sonra sayfayı yenileyin.'; } catch (e) { msg.className = 'msg error'; msg.textContent = e.message; }
    } }, 'Zafiyet kayıtlarını yenile') : null;
    const advBox = !adv.enabled
      ? h('div', { class: 'panel' }, h('b', null, 'Zafiyet (CVE) eşleme kapalı. '), 'Sunucuda ', h('code', null, 'APM_ADVISORY_DIR'), ' (veya veri dizini) tanımlayın; dizine OSV kayıtlarını (osv.dev ekosistem arşivleri: npm, Packagist, PyPI, Maven, Go, NuGet "all.zip") koyun ya da internete çıkabilen sunucuda ', h('code', null, 'APM_ADVISORY_AUTO=1'), ' verin.')
      : adv.records ? h('div', { class: 'panel small' }, `Zafiyet kaydı: ${adv.records} kayıt yüklü` + (adv.loaded_at ? ' · ' + ago(adv.loaded_at) : '') + (adv.auto ? ' · günlük otomatik indirme açık' : ' · elle yönetilen dizin: ' + adv.dir) + (adv.error ? ' · ' + adv.error : ''), ' ', reload)
        : h('div', { class: 'panel' }, h('b', null, adv.loading ? 'Zafiyet kayıtları yükleniyor… ' : 'Zafiyet kayıt dizini boş. '), adv.auto ? 'Otomatik indirme açık; ilk indirme birkaç dakika sürebilir.' : h('span', null, `${adv.dir} dizinine OSV arşivlerini koyun (npm, Packagist, PyPI, Maven, Go, NuGet) ya da APM_ADVISORY_AUTO=1 verin.`), ' ', reload);
    if (SURF.inv != null && !d.assets.some((a) => a.asset_id === SURF.inv)) SURF.inv = null;
    if (SURF.inv != null) {
      const a = d.assets.find((x) => x.asset_id === SURF.inv);
      const back = h('button', { type: 'button', onclick: () => { SURF.inv = null; render(); } }, '← Tüm makineler');
      return h('div', { class: 'stack' }, h('div', { class: 'row' }, back), h('h3', null, a.asset), invDetail(a, adv, msg), msg);
    }
    const rows = d.assets.map((a) => {
      const ins = invInsight(a, adv);
      const tr = h('tr', { class: 'clickrow', tabindex: '0' }, h('td', null, h('b', null, a.asset), h('div', { class: 'muted small' }, 'envanter ' + ago(a.updated_at))),
        h('td', { class: 'mono' }, `${a.listeners.length}`, h('span', { class: 'muted small' }, ` · ${a.listeners.filter((l) => l.scope === 'net').length} ağdan`)),
        h('td', { class: 'mono' }, a.eol_count ? pill('down', String(a.eol_count)) : '0'), h('td', { class: 'mono' }, String(a.deps_count)),
        h('td', { class: 'mono' }, a.critical ? pill('down', a.critical + ' kritik') : a.high ? pill('warning', a.high + ' yüksek') : '0'), insightCell(ins));
      const go = () => { SURF.inv = a.asset_id; render(); };
      tr.addEventListener('click', go); tr.addEventListener('keydown', (e) => { if (e.key === 'Enter') go(); });
      return tr;
    });
    return h('div', { class: 'stack' },
      h('p', { class: 'muted small' }, 'Ajan; hangi portun hangi süreç tarafından dinlendiğini, çalışma zamanının (Node.js, Java, PHP, Python, nginx, PostgreSQL…) sürümünü ve uygulama bağımlılıklarını (paket adı + sürüm) 30 dakikada bir bildirir. Dosya içeriği gönderilmez. Makineye tıklayınca ayrıntı açılır.'),
      h('div', { class: 'kpis' }, kpi('Dinleyen servis', String(t.listeners || 0), `${t.exposed || 0} ağdan erişilebilir`, null, ''), kpi('Destek dışı', String(t.eol || 0), 'sürüm', null, t.eol ? 'warn' : ''),
        kpi('Bağımlılık', String(t.deps || 0), 'paket', null, ''), kpi('Zafiyet', String((t.critical || 0) + (t.high || 0)), `${t.critical || 0} kritik · ${t.high || 0} yüksek`, null, t.critical ? 'bad' : t.high ? 'warn' : '')),
      advBox, msg,
      table(['Makine', 'Dinleyen servis', 'Destek dışı', 'Bağımlılık', 'Zafiyet', 'Yorum'], rows, 'Henüz envanter yok. Ajan (v1.6.0+) Ajanlar sayfasında "Saldırı yüzeyi envanteri" veri türü açıkken ~20 saniye içinde ilk envanteri gönderir; ajanı güncelleyin.', PAGE),
      h('p', { class: 'muted small' }, 'Zafiyet eşleme, kayıtlardaki paket adı ve sürüm aralığına dayanır; bir tarayıcı değildir, sonuçlar "olası" kabul edilmelidir. Java bağımlılıkları jar dosya adından, package.json ile bildirilen sürümler ise kilit dosyası olmadığında tahmini çıkarılır. Destek durumu üretici takvimine göredir (dağıtıcının uzatılmış desteği farklı olabilir).'));
  }
  const SURF_TABS = [['drift', 'Sapmalar'], ['inv', 'Envanter']];
  async function viewYuzey() {
    await refreshHeader();
    const tabs = h('div', { class: 'seg', role: 'group', 'aria-label': 'Saldırı yüzeyi sekmeleri' }, SURF_TABS.map(([id, label]) => {
      const b = h('button', { type: 'button', 'aria-pressed': String(SURF.tab === id) }, label);
      b.addEventListener('click', () => { SURF.tab = id; SURF.inv = null; render(); });
      return b;
    }));
    const body = await (SURF.tab === 'inv' ? yuzeyEnvanter : yuzeyDrift)();
    return h('section', { class: 'stack' }, h('h2', null, 'Saldırı yüzeyi'), tabs, body);
  }

  // ---- Kanıt zinciri (v2.6.0) ----
  async function saveBlob(path, name) {
    const res = await fetch(path, { headers: { 'X-API-Key': key } });
    if (!res.ok) throw new Error('HTTP ' + res.status);
    const a = h('a', { href: URL.createObjectURL(await res.blob()), download: name });
    document.body.append(a); a.click(); a.remove();
  }

  async function viewKanit() {
    await refreshHeader();
    const list = await api('/api/evidence/reports');
    const msg = h('p', { class: 'msg', role: 'status' });
    const chainBox = h('div', { class: 'panel stack', 'aria-live': 'polite' }, h('p', { class: 'muted' }, 'Denetim günlüğü zincirini doğrulamak için düğmeye basın.'));
    const chainBtn = h('button', { type: 'button', class: 'primary', onclick: async () => {
      try {
        const c = await api('/api/evidence/chain');
        chainBox.replaceChildren(c.ok
          ? h('p', null, pill('up', 'Zincir sağlam'), ' ', `${c.checked} kayıt doğrulandı`, c.legacy ? [' · ', `zincir öncesi ${c.legacy} eski kayıt (doğrulanamaz)`] : null, c.head_hash ? [' · ', `zincir başı ${c.head_hash.slice(0, 16)}…`] : null)
          : h('p', null, pill('down', 'Zincir bozuk'), ' ', `Kayıt #${c.broken_id}: `, c.reason));
      } catch (e) { chainBox.replaceChildren(h('p', { class: 'msg error' }, e.message)); }
    } }, 'Zinciri doğrula');
    const months = [];
    for (let i = 0, d = new Date(); i < 12; i++, d = new Date(d.getFullYear(), d.getMonth() - 1, 1)) months.push(d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0'));
    const per = select(null, months.map((m, i) => [m, m + (i === 0 ? ' (süren dönem)' : '')]), months[1]);
    const gen = h('button', { type: 'button', class: 'primary', onclick: async () => {
      gen.disabled = true; msg.className = 'msg'; msg.textContent = '';
      try { await api('/api/evidence/reports', { method: 'POST', body: { period: per.value } }); render(); } catch (e) { gen.disabled = false; msg.className = 'msg error'; msg.textContent = e.message; }
    } }, 'Rapor üret ve imzala');
    const rows = list.map((r) => h('tr', null, h('td', null, h('b', null, r.label)), h('td', { class: 'mono small' }, ago(r.created_at)), h('td', null, r.created_by),
      h('td', null, r.chain_ok ? pill('up', 'Sağlam') : pill('down', 'Bozuk')), h('td', { class: 'mono small' }, r.head_hash ? r.head_hash.slice(0, 12) + '…' : '—'), h('td', { class: 'mono small' }, r.key_id),
      h('td', null, h('button', { type: 'button', class: 'small', onclick: async () => { try { await saveBlob(`/api/evidence/reports/${r.id}/package`, `appmon-kanit-${r.label}.json`); } catch (e) { msg.className = 'msg error'; msg.textContent = e.message; } } }, 'İndir (.json)'))));
    const vres = h('div', { class: 'stack', 'aria-live': 'polite' });
    const file = h('input', { type: 'file', accept: '.json,application/json' });
    file.addEventListener('change', async () => {
      const f = file.files[0]; if (!f) return;
      try {
        const m = await api('/api/evidence/verify', { method: 'POST', body: JSON.parse(await f.text()) });
        const row = (ok, t) => h('li', null, ok === true ? pill('up', 'Tamam') : ok === false ? pill('down', 'Sorun') : pill('unknown', 'Bilgi'), ' ', t);
        vres.replaceChildren(h('p', null, h('b', null, `Dönem ${m.label} · ${m.tenant}`), ' · ' + new Date(m.generated_at).toLocaleString((window.LOCALE || 'tr-TR'))), h('ul', { class: 'plain' },
          row(m.signature_valid, m.signature_valid ? 'İmza geçerli: rapor imzalandığından beri değişmemiş.' : 'İmza GEÇERSİZ: rapor değiştirilmiş veya bozuk.'),
          row(m.key_is_this_server === undefined ? null : m.key_is_this_server, m.key_is_this_server ? 'İmza anahtarı bu sunucuya ait.' : 'İmza anahtarı bu sunucuya ait değil.'),
          row(m.chain_ok_at_creation, m.chain_ok_at_creation ? 'Rapor üretilirken denetim zinciri sağlamdı.' : 'Rapor üretilirken denetim zinciri bozuktu.'),
          m.chain_match === undefined ? row(null, m.chain_note || '') : row(m.chain_match, m.chain_match ? 'Raporun zincir başı bugünkü kayıtlarla eşleşiyor: o tarihten bu yana kayıtlar değişmemiş.' : (m.chain_note || 'Zincir başı eşleşmiyor.')),
          m.chain_ok_now === undefined ? null : row(m.chain_ok_now, m.chain_ok_now ? 'Zincir şu anda da sağlam.' : 'Zincir şu anda bozuk.')));
      } catch (e) { vres.replaceChildren(h('p', { class: 'msg error' }, e.message)); }
    });
    return h('section', { class: 'stack' }, h('h2', null, 'Kanıt zinciri'),
      h('p', { class: 'muted small' }, 'Denetim günlüğü SHA-256 zinciriyle bağlanır: bir kayıt sonradan değiştirilir, araya kayıt eklenir veya silinirse zincir kırılır. Aylık kanıt raporu (kullanılabilirlik, olaylar, yapılan değişiklikler, zincir başı) imzalanır; raporu sunucu dışında saklayan biri, kayıtların sonradan değiştirilmediğini ispatlayabilir.'),
      msg,
      h('h3', null, 'Zincir durumu'), h('div', { class: 'row' }, chainBtn), chainBox,
      h('h3', null, 'İmzalı rapor üret'), h('div', { class: 'row' }, h('label', null, 'Dönem ', per), gen),
      table(['Dönem', 'Üretildi', 'Üreten', 'Zincir', 'Zincir başı', 'Anahtar', ''], rows, 'Henüz rapor yok.', PAGE),
      h('h3', null, 'Raporu doğrula'), h('p', { class: 'muted small' }, 'İndirdiğiniz paketi (.json) seçin: imza ve zincir başı bugünkü kayıtlarla karşılaştırılır.'), file, vres,
      h('details', { class: 'panel' }, h('summary', null, 'Sunucudan bağımsız doğrulama (OpenSSL)'),
        h('p', { class: 'muted small' }, 'Genel anahtarı indirin, paketten rapor ve imzayı çıkarıp OpenSSL 3 ile doğrulayın:'),
        h('button', { type: 'button', class: 'small', onclick: async () => { try { await saveBlob('/api/evidence/public-key', 'appmon-kanit-anahtar.pem'); } catch (e) { msg.className = 'msg error'; msg.textContent = e.message; } } }, 'Genel anahtarı indir (.pem)'),
        h('pre', { class: 'perf-code' }, h('code', null, "jq -r .report_json paket.json > rapor.json\njq -r .signature paket.json | base64 -d > imza.bin\nopenssl pkeyutl -verify -pubin -inkey appmon-kanit-anahtar.pem -rawin -in rapor.json -sigfile imza.bin"))));
  }

  // Raporlar (önizlemeli, yazdırılabilir): reports.js; eskiden Ayarlar sayfasındaydı.
  async function viewRaporlar() {
    await refreshHeader();
    const sec = window.APM && window.APM.reports ? window.APM.reports.section() : null;
    return h('div', null, h('h2', null, 'Raporlar'), sec || h('p', { class: 'muted' }, 'Rapor modülü yüklenemedi.'));
  }

  // ---- Dağıtık izleme (v3.0.0): OTLP/HTTP trace alımı, trace listesi + şelale, servis özeti, servis haritası ----
  const TRACE = { tab: 'list', src: 'all', since: '1h', service: '', status: '', q: '', min: '', sort: 'recent', lsvc: '', llevel: 'warn', lq: '' };
  const TRACE_TABS = [['list', 'Trace\'ler'], ['services', 'Servisler'], ['map', 'Servis haritası'], ['logs', 'Loglar']];
  const TRACE_SINCE = [['15m', 'Son 15 dk'], ['1h', 'Son 1 saat'], ['6h', 'Son 6 saat'], ['24h', 'Son 24 saat'], ['7d', 'Son 7 gün']];
  const SPAN_KIND = { 0: 'belirsiz', 1: 'iç', 2: 'sunucu', 3: 'istemci', 4: 'üretici', 5: 'tüketici' };
  function fmtUs(us) {
    if (us == null || isNaN(us)) return '—';
    if (us < 1000) return Math.round(us) + ' µs';
    if (us < 1e6) return (us / 1000).toFixed(us < 1e4 ? 1 : 0) + ' ms';
    return (us / 1e6).toFixed(2) + ' sn';
  }
  function svcColor(name) {
    let x = 0;
    for (const c of name) x = (x * 31 + c.charCodeAt(0)) >>> 0;
    return 'hsl(' + (x % 360) + ',55%,45%)';
  }
  function traceSetup() {
    const url = location.origin + '/v1/traces';
    const sh = location.origin + '/kurulum/otel-kurulum.sh', ps = location.origin + '/kurulum/otel-kurulum.ps1';
    return h('details', { class: 'card pad' }, h('summary', null, 'Trace nasıl gönderilir? (OpenTelemetry, agent\'sız)'),
      h('p', { class: 'small' }, 'Uygulamanızdaki OpenTelemetry SDK\'sını (veya bir OpenTelemetry Collector\'ı) bu sunucuya yönlendirin; trace\'ler OTLP/HTTP ile (JSON veya protobuf, gzip desteklenir) alınır ve servisler otomatik asset olarak kaydedilir.'),
      h('h4', null, 'En kolay yol: kurulum betiği'),
      h('p', { class: 'small' }, 'Betik, hedef sistemde bağlantıyı ve anahtarı dener, deneme trace\'i gönderir ve istenirse servisi (Linux: systemd, Windows: servis veya IIS) OpenTelemetry için yapılandırır. Önce "Lisans ve anahtarlar" sayfasından agent rolünde bir anahtar oluşturun.'),
      h('p', { class: 'small' }, h('b', null, 'Linux'), ' (root): ', h('a', { href: sh, download: 'otel-kurulum.sh' }, 'otel-kurulum.sh indir')),
      h('pre', { class: 'mono small' }, 'curl -fsSL ' + sh + ' -o otel-kurulum.sh && chmod +x otel-kurulum.sh\nsudo ./otel-kurulum.sh --service siparis --key <anahtar> --unit siparis --restart'),
      h('p', { class: 'small' }, h('b', null, 'Windows'), ' (yönetici PowerShell): ', h('a', { href: ps, download: 'otel-kurulum.ps1' }, 'otel-kurulum.ps1 indir')),
      h('pre', { class: 'mono small' }, 'Invoke-WebRequest ' + ps + ' -OutFile otel-kurulum.ps1\npowershell -ExecutionPolicy Bypass -File .\\otel-kurulum.ps1 -Service siparis -Key <anahtar> -WindowsService SiparisSvc -Language dotnet -Restart'),
      h('p', { class: 'muted small' }, 'Yalnızca bağlantı denemesi için --unit / -WindowsService vermeyin. IIS için -IIS, geri almak için --undo / -Undo. Java, Node, Python ve .NET için gereken ajan/paket (internet gerekir) betik tarafından kurulur.'),
      h('h4', null, 'Elle kurulum'),
      h('pre', { class: 'mono small' }, 'OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=' + url + '\nOTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/protobuf\nOTEL_EXPORTER_OTLP_HEADERS=X-API-Key=<agent anahtarı>\nOTEL_SERVICE_NAME=siparis-servisi'),
      h('p', { class: 'muted small' }, 'Trace verisi en fazla 14 gün saklanır. Servisler arası çağrılar ve veritabanı/dış servis bağımlılıkları "Servis haritası" sekmesinde görünür.'));
  }
  function waterfall(det) {
    const total = Math.max(det.dur_us, 1);
    const kids = new Map();
    const ids = new Set(det.spans.map((x) => x.span_id));
    for (const sp of det.spans) { const p = ids.has(sp.parent_id) ? sp.parent_id : ''; if (!kids.has(p)) kids.set(p, []); kids.get(p).push(sp); }
    const rows = [];
    const walk = (p, depth) => { for (const sp of kids.get(p) || []) { rows.push([sp, depth]); walk(sp.span_id, depth + 1); } };
    walk('', 0);
    const detail = h('div', { class: 'trace-detail muted small' }, 'Ayrıntı için bir span\'e tıklayın.');
    const list = h('div', { class: 'wf' }, rows.map(([sp, depth]) => {
      const bar = h('div', { class: 'wf-bar' + (sp.status === 2 ? ' err' : '') });
      bar.style.left = (sp.offset_us / total * 100).toFixed(2) + '%';
      bar.style.width = Math.max(0.4, sp.dur_us / total * 100).toFixed(2) + '%';
      if (sp.status !== 2) bar.style.background = svcColor(sp.service);
      const name = h('div', { class: 'wf-name' }, h('span', { class: 'wf-ind' }), sp.name, h('span', { class: 'muted small' }, ' · ' + sp.service));
      name.firstChild.style.width = (depth * 14) + 'px';
      const row = h('div', { class: 'wf-row', tabindex: '0', role: 'button' }, name, h('div', { class: 'wf-track' }, bar), h('div', { class: 'wf-dur mono small' }, fmtUs(sp.dur_us)));
      const show = () => {
        const at = Object.entries(sp.attrs || {});
        detail.replaceChildren(h('b', null, sp.name), h('span', { class: 'muted' }, ` — ${sp.service} · ${SPAN_KIND[sp.kind] || sp.kind} · ${fmtUs(sp.dur_us)}${sp.status === 2 ? ' · HATA' : ''}`),
          sp.status_msg && sp.status === 2 ? h('div', { class: 'msg error' }, sp.status_msg) : h('span'),
          at.length ? h('table', { class: 'kv' }, at.map(([k, v]) => h('tr', null, h('td', { class: 'mono small' }, k), h('td', { class: 'mono small' }, v)))) : h('div', { class: 'muted' }, 'Öznitelik yok.'));
      };
      row.addEventListener('click', show); row.addEventListener('keydown', (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); show(); } });
      return row;
    }));
    const svcs = [...new Set(det.spans.map((x) => x.service))];
    const legend = h('div', { class: 'wf-legend small' }, svcs.map((n) => { const d = h('span', { class: 'wf-dot' }); d.style.background = svcColor(n); return h('span', null, d, ' ' + n); }));
    return h('div', null, h('p', { class: 'muted small' }, `${det.spans.length} span · ${svcs.length} servis · toplam ${fmtUs(det.dur_us)}`), legend, list, detail);
  }
  const LOG_LEVEL = { debug: ['unknown', 'Debug'], info: ['up', 'Bilgi'], warn: ['warning', 'Uyarı'], error: ['critical', 'Hata'], fatal: ['critical', 'Ölümcül'] };
  const logRows = (list, withTrace) => list.map((x) => h('tr', withTrace ? { class: 'clickable', tabindex: '0', onclick: () => x.trace_id && openTrace(x.trace_id) } : null,
    h('td', { class: 'mono small' }, ago(x.time)), h('td', null, pill(...(LOG_LEVEL[x.level] || LOG_LEVEL.info))), h('td', { class: 'small' }, x.service), h('td', { class: 'small' }, x.message),
    withTrace ? h('td', { class: 'mono small' }, x.trace_id ? x.trace_id.slice(0, 12) + '…' : '—') : null));
  async function traceLogsSection(id) {
    const r = await api('/api/traces/' + id + '/logs').catch(() => null);
    if (!r) return null;
    const head = h('h3', null, 'İlgili loglar');
    if (!r.enabled) return h('div', { class: 'stack' }, head, h('p', { class: 'muted small' }, 'Log saklama kapalı. Açmak için sunucuda APM_LOG_EVENTS=1 verin ve uygulamanızdan OTLP günlükleri (POST /v1/logs) gönderin.'));
    if (!r.exact.length && !r.near.length) return h('div', { class: 'stack' }, head, h('p', { class: 'muted small' }, 'Bu trace ile eşleşen log yok (varsayılan olarak yalnızca uyarı ve üstü saklanır; logda trace_id bulunmalıdır).'));
    return h('div', { class: 'stack' }, head,
      r.exact.length ? table(['Zaman', 'Seviye', 'Servis', 'Mesaj'], logRows(r.exact, false), '') : h('p', { class: 'muted small' }, 'trace_id ile eşleşen log yok.'),
      r.near.length ? h('div', { class: 'stack' }, h('p', { class: 'muted small' }, 'Yaklaşık eşleşme: aynı servislerde, trace süresince (±2 sn) kaydedilen, trace_id\'si olmayan loglar.'), table(['Zaman', 'Seviye', 'Servis', 'Mesaj'], logRows(r.near, false), '')) : null);
  }
  async function openTrace(id) {
    try {
      const [det, logs] = await Promise.all([api('/api/traces/' + id), traceLogsSection(id)]);
      openModal('Trace ' + id.slice(0, 16) + '…', h('div', { class: 'stack' }, waterfall(det), logs), false, true);
    } catch (e) { alert(e.message); }
  }
  async function traceLogsTab() {
    const qs = new URLSearchParams({ since: TRACE.since, level: TRACE.llevel });
    if (TRACE.lsvc) qs.set('service', TRACE.lsvc);
    if (TRACE.lq) qs.set('q', TRACE.lq);
    const res = await api('/api/logs?' + qs);
    const mk = (key, el) => { el.addEventListener('change', () => { TRACE[key] = el.value; render(); }); return el; };
    const since = mk('since', select(null, TRACE_SINCE, TRACE.since));
    const svc = mk('lsvc', select(null, [['', 'Servis: tümü']].concat((res.services || []).map((x) => [x, x])), TRACE.lsvc));
    const lvl = mk('llevel', select(null, [['debug', 'Seviye: tümü'], ['info', 'Bilgi ve üstü'], ['warn', 'Uyarı ve üstü'], ['error', 'Hata ve üstü']], TRACE.llevel));
    const q = h('input', { type: 'search', placeholder: 'Mesajda ara', value: TRACE.lq, 'aria-label': 'Mesajda ara' });
    q.addEventListener('change', () => { TRACE.lq = q.value; render(); });
    const note = !res.enabled
      ? h('div', { class: 'panel' }, h('b', null, 'Log saklama kapalı. '), 'Açmak için sunucuda ', h('code', null, 'APM_LOG_EVENTS=1'), ' verip yeniden başlatın. Uygulamanız OpenTelemetry kullanıyorsa günlükleri ', h('code', null, 'POST /v1/logs'), ' (OTLP/HTTP) ile gönderin; başka bir araç için ', h('code', null, 'POST /api/logs'), ' ({"logs":[{"service","level","message","trace_id"}]}). Trace kimliği olan satırlar trace ayrıntısında görünür.')
      : h('p', { class: 'muted small' }, `Yalnızca ${LOG_LEVEL[res.min_level] ? LOG_LEVEL[res.min_level][1] : res.min_level} ve üstü saklanır; mesajlar kısaltılır, e-posta/parola/token gibi değerler maskelenir; ${res.retention_days} gün tutulur. Satıra tıklayınca ilgili trace açılır.`);
    return h('div', { class: 'stack' }, h('div', { class: 'filters' }, since, svc, lvl, q), note,
      table(['Zaman', 'Seviye', 'Servis', 'Mesaj', 'Trace'], logRows(res.logs || [], true), 'Bu aralıkta log yok.', 25));
  }
  async function traceList() {
    const qs = new URLSearchParams({ since: TRACE.since, sort: TRACE.sort });
    for (const k of ['service', 'status', 'q', 'min']) if (TRACE[k]) qs.set(k === 'min' ? 'min_ms' : k, TRACE[k]);
    const [res, sv] = await Promise.all([api('/api/traces?' + qs), api('/api/traces/services?since=' + TRACE.since)]);
    const mk = (key, el) => { el.addEventListener('change', () => { TRACE[key] = el.value; render(); }); return el; };
    const svc = mk('service', select(null, [['', 'Servis: tümü']].concat(sv.services.map((x) => [x.service, x.service])), TRACE.service));
    const st = mk('status', select(null, [['', 'Durum: tümü'], ['error', 'Yalnızca hatalı']], TRACE.status));
    const sort = mk('sort', select(null, [['recent', 'En yeni'], ['slow', 'En yavaş']], TRACE.sort));
    const since = mk('since', select(null, TRACE_SINCE, TRACE.since));
    const q = h('input', { type: 'search', placeholder: 'Trace kimliği veya işlem adı', value: TRACE.q, 'aria-label': 'Ara' });
    q.addEventListener('change', () => { TRACE.q = q.value; render(); });
    const min = h('input', { type: 'number', min: '0', placeholder: 'En az ms', value: TRACE.min, class: 'sla-hour', 'aria-label': 'En az süre (ms)' });
    min.addEventListener('change', () => { TRACE.min = min.value; render(); });
    const rows = (res.traces || []).map((t) => h('tr', { class: 'clickable', tabindex: '0', onclick: () => openTrace(t.trace_id) },
      h('td', { class: 'mono small' }, ago(t.start)), h('td', null, t.root_name || '—', h('div', { class: 'muted small' }, t.root_service || '')),
      h('td', { class: 'mono' }, fmtUs(t.dur_us)), h('td', null, String(t.spans)), h('td', { class: 'small' }, t.services.join(', ')),
      h('td', null, t.errors ? pill('critical', t.errors + ' hata') : pill('up', 'tamam')), h('td', { class: 'mono small' }, t.trace_id.slice(0, 12) + '…')));
    return h('div', { class: 'stack' }, h('div', { class: 'filters' }, since, svc, st, sort, q, min),
      table(['Zaman', 'Kök işlem', 'Süre', 'Span', 'Servisler', 'Durum', 'Trace'], rows, 'Bu aralıkta trace yok. Aşağıdaki yönergeyle OpenTelemetry trace göndermeye başlayın.', 25), traceSetup());
  }
  // Servis yorumu (RED): hata oranı, gecikme ve veri tazeliği.
  function serviceInsight(x, er) {
    const last = Date.parse(x.last);
    if (er >= 5) return { cls: 'down', head: 'Hata oranı yüksek', text: 'Çağrıların önemli bölümü hata ile bitiyor. Satıra tıklayıp hangi işlemin hata verdiğine, sonra Trace listesinde hatalı izlere bakın.' };
    if (x.p95_us >= 2000000) return { cls: 'down', head: 'Yanıt çok yavaş', text: 'Çağrıların %5\'i 2 sn\'den uzun sürüyor. İşlem ayrıntısında en yavaş işlemi bulun; izdeki en uzun span darboğazı gösterir.' };
    if (er > 0) return { cls: 'warning', head: 'Zaman zaman hata', text: 'Düşük oranda hata var; tek seferlik olabilir. Sürerse hatalı izleri inceleyin.' };
    if (x.p95_us >= 800000) return { cls: 'warning', head: 'Gecikme yüksek', text: 'p95 süresi 800 ms\'yi aşıyor; en yavaş işlemi ve bağımlı çağrıları kontrol edin.' };
    if (last && Date.now() - last > 15 * 60 * 1000) return { cls: 'unknown', head: 'Sessiz', text: 'Son 15 dakikada bu servisten yeni çağrı gelmedi; trafik yok ya da gönderim durmuş olabilir.' };
    return { cls: 'up', head: 'Sağlıklı', text: 'Hata oranı ve süreler normal.' };
  }
  async function traceServices() {
    const sv = await api('/api/traces/services?since=' + TRACE.since);
    const since = select(null, TRACE_SINCE, TRACE.since);
    since.addEventListener('change', () => { TRACE.since = since.value; render(); });
    const rows = (sv.services || []).map((x) => {
      const er = x.calls ? x.errors / x.calls * 100 : 0;
      return h('tr', { class: 'clickable', tabindex: '0', onclick: async () => {
        const o = await api('/api/traces/operations?service=' + encodeURIComponent(x.service) + '&since=' + TRACE.since);
        openModal(x.service + ' — işlemler', table(['İşlem', 'Çağrı', 'Hata', 'p50', 'p95'], o.operations.map((p) => h('tr', null, h('td', { class: 'mono small' }, p.name), h('td', null, String(p.calls)), h('td', null, String(p.errors)), h('td', { class: 'mono' }, fmtUs(p.p50_us)), h('td', { class: 'mono' }, fmtUs(p.p95_us)))), 'İşlem yok.'), false, true);
      } }, h('td', null, x.service), h('td', null, String(x.calls)), h('td', { class: 'mono' }, x.rate_per_min.toFixed(1) + '/dk'),
      h('td', null, er >= 5 ? pill('critical', '%' + er.toFixed(1)) : er > 0 ? pill('warning', '%' + er.toFixed(1)) : pill('up', '%0')),
      h('td', { class: 'mono' }, fmtUs(x.p50_us)), h('td', { class: 'mono' }, fmtUs(x.p95_us)), h('td', { class: 'mono' }, fmtUs(x.p99_us)), insightCell(serviceInsight(x, er)), h('td', { class: 'mono small' }, ago(x.last)));
    });
    return h('div', { class: 'stack' }, h('div', { class: 'filters' }, since),
      h('p', { class: 'muted small' }, 'RED özeti: servisin dışarıdan aldığı çağrılar (sunucu/kök span\'lar) — hız, hata oranı ve süre yüzdelikleri. Ayrıntı için satıra tıklayın.'),
      table(['Servis', 'Çağrı', 'Hız', 'Hata oranı', 'p50', 'p95', 'p99', 'Yorum', 'Son'], rows, 'Henüz trace verisi yok.', 25), traceSetup());
  }
  async function traceMap() {
    const [m, sv] = await Promise.all([api('/api/traces/map?since=' + TRACE.since), api('/api/traces/services?since=' + TRACE.since).catch(() => ({ services: [] }))]);
    const since = select(null, TRACE_SINCE, TRACE.since);
    since.addEventListener('change', () => { TRACE.since = since.value; render(); });
    const src = select(null, [['all', 'Kaynak: birleşik'], ['trace', 'Yalnızca trace'], ['conn', 'Yalnızca bağlantı (ajan)']], TRACE.src);
    src.addEventListener('change', () => { TRACE.src = src.value; render(); });
    const edges = [];
    if (TRACE.src !== 'conn') for (const e of m.edges) edges.push(e);
    if (TRACE.src !== 'trace') for (const e of (m.conn_edges || [])) edges.push({ from: e.from, to: e.to, calls: e.conns, errors: 0, p95_us: 0, external: e.external, conn: true });
    if (!edges.length) return h('div', { class: 'stack' }, h('div', { class: 'filters' }, since, src), h('p', { class: 'muted' }, 'Bu aralıkta servisler arası çağrı veya bağlantı yok. Harita, trace\'lerden ve ajanların gözlemlediği giden TCP bağlantılarından çıkarılır.'), traceSetup());
    const nodes = new Map(), indeg = new Map();
    for (const e of edges) {
      for (const n of [e.from, e.to]) if (!nodes.has(n)) { nodes.set(n, { name: n, ext: false }); indeg.set(n, 0); }
      if (e.external) nodes.get(e.to).ext = !edges.some((x) => !x.external && (x.from === e.to || x.to === e.to));
      indeg.set(e.to, indeg.get(e.to) + 1);
    }
    const depth = new Map();
    let frontier = [...nodes.keys()].filter((n) => indeg.get(n) === 0);
    if (!frontier.length) frontier = [[...nodes.keys()][0]];
    frontier.forEach((n) => depth.set(n, 0));
    for (let d = 0; frontier.length && d < 12; d++) {
      const nx = [];
      for (const e of edges) if (frontier.includes(e.from) && !depth.has(e.to)) { depth.set(e.to, d + 1); nx.push(e.to); }
      frontier = nx;
    }
    for (const n of nodes.keys()) if (!depth.has(n)) depth.set(n, 0);
    const cols = new Map();
    for (const n of nodes.keys()) { const d = depth.get(n); if (!cols.has(d)) cols.set(d, []); cols.get(d).push(n); }
    // Düğüm durumu: servisin aldığı çağrılar (RED özeti; yoksa gelen oklar) üzerinden hata oranı ve p95.
    const svcStat = new Map((sv.services || []).map((x) => [x.service, x]));
    const stat = (n) => {
      const x = svcStat.get(n);
      let calls = 0, errors = 0, p95 = 0;
      if (x) { calls = x.calls; errors = x.errors; p95 = x.p95_us || 0; }
      else for (const e of edges) if (e.to === n && !e.conn) { calls += e.calls; errors += e.errors; p95 = Math.max(p95, e.p95_us || 0); }
      const er = calls ? errors / calls * 100 : 0;
      const cls = !calls ? 'none' : er >= 5 ? 'bad' : p95 >= 2000000 ? 'danger' : (er > 0 || p95 >= 800000) ? 'warn' : 'ok';
      return { calls, errors, p95, er, cls };
    };
    const TM_WORD = { bad: 'hata oranı yüksek', danger: 'çok yavaş', warn: 'uyarı', ok: 'sağlıklı', none: 'ölçüm yok' };
    const CH = 50, GXX = 150, GYY = 40, P = 24;
    const wOf = (n) => Math.max(150, Math.round(n.length * 7.4) + 28);
    const colW = new Map(), colX = new Map();
    for (const [d, list] of cols) colW.set(d, Math.max(...list.map(wOf)));
    let cx = P;
    for (const d of [...cols.keys()].sort((x, y) => x - y)) { colX.set(d, cx); cx += colW.get(d) + GXX; }
    const maxRows = Math.max(...[...cols.values()].map((c) => c.length));
    const pos = new Map();
    let saved = {};
    try { saved = JSON.parse(localStorage.getItem('apm_tmap_pos') || '{}') || {}; } catch (e) { saved = {}; }
    for (const [d, list] of cols) list.forEach((n, i) => {
      const sp = saved[n];
      pos.set(n, sp && isFinite(sp.x) && isFinite(sp.y) ? { x: Math.max(0, sp.x), y: Math.max(0, sp.y) }
        : { x: colX.get(d) + (colW.get(d) - wOf(n)) / 2, y: P + i * (CH + GYY) + (maxRows - list.length) * (CH + GYY) / 2 });
    });
    const svg = s('svg', { class: 'tmap', role: 'group', 'aria-label': 'Servis haritası (kutuları sürükleyerek yerleştirebilirsiniz)' });
    const fit = () => {
      let W = 0, H = 0;
      for (const [n, p] of pos) { W = Math.max(W, p.x + wOf(n)); H = Math.max(H, p.y + CH); }
      W += P; H += P;
      svg.setAttribute('width', W); svg.setAttribute('height', H); svg.setAttribute('viewBox', `0 0 ${W} ${H}`);
    };
    const mx = Math.max(...edges.map((e) => e.calls));
    const edgeEls = edges.map((e) => {
      const bad = e.calls && e.errors / e.calls >= 0.05;
      const path = s('path', { class: 'tedge' + (bad ? ' bad' : '') + (e.conn ? ' conn' : ''), 'stroke-width': (1 + 3 * e.calls / mx).toFixed(1) });
      const lab = s('text', { class: 'tlabel', 'text-anchor': 'middle' }, e.conn ? `${e.calls} bağlantı` : `${e.calls} çağrı · p95 ${fmtUs(e.p95_us)}`);
      const err = e.errors ? s('text', { class: 'tlabel bad', 'text-anchor': 'middle' }, `${e.errors} hata`) : null;
      svg.append(path, lab); if (err) svg.append(err);
      return { e, path, lab, err };
    });
    const placeEdges = () => {
      for (const { e, path, lab, err } of edgeEls) {
        const a = pos.get(e.from), b = pos.get(e.to);
        const fwd = a.x + wOf(e.from) <= b.x + 10;
        const x1 = fwd ? a.x + wOf(e.from) : a.x + wOf(e.from) / 2, y1 = fwd ? a.y + CH / 2 : (b.y > a.y ? a.y + CH : a.y);
        const x2 = fwd ? b.x : b.x + wOf(e.to) / 2, y2 = fwd ? b.y + CH / 2 : (b.y > a.y ? b.y : b.y + CH);
        path.setAttribute('d', fwd ? `M${x1} ${y1}C${x1 + 50} ${y1} ${x2 - 50} ${y2} ${x2} ${y2}` : `M${x1} ${y1}L${x2} ${y2}`);
        lab.setAttribute('x', (x1 + x2) / 2); lab.setAttribute('y', (y1 + y2) / 2 - 9);
        if (err) { err.setAttribute('x', (x1 + x2) / 2); err.setAttribute('y', (y1 + y2) / 2 + 4); }
      }
    };
    const toSvg = (ev) => { const pt = svg.createSVGPoint(); pt.x = ev.clientX; pt.y = ev.clientY; return pt.matrixTransform(svg.getScreenCTM().inverse()); };
    const persist = () => { const m = {}; for (const [n, p] of pos) m[n] = { x: Math.round(p.x), y: Math.round(p.y) }; try { localStorage.setItem('apm_tmap_pos', JSON.stringify(m)); } catch (e) { /* depolama kapalı */ } };
    for (const [n, nd] of nodes) {
      const st = stat(n), w = wOf(n);
      const sub = nd.ext && !st.calls ? 'dış bağımlılık' : !st.calls ? 'ölçüm yok' : `p95 ${fmtUs(st.p95)} · hata %${st.er.toFixed(st.er && st.er < 10 ? 1 : 0)}`;
      const g = s('g', { class: 'tnode-g', tabindex: '0', role: 'button', 'aria-label': `${n}, ${TM_WORD[st.cls]}` },
        s('title', null, `${n}\n${TM_WORD[st.cls]}${st.calls ? `\n${st.calls} çağrı · ${st.errors} hata · p95 ${fmtUs(st.p95)}` : ''}`),
        s('rect', { class: 'tnode ' + st.cls + (nd.ext ? ' ext' : ''), x: 0, y: 0, width: w, height: CH, rx: 6 }),
        s('text', { class: 'tname', x: w / 2, y: 21, 'text-anchor': 'middle' }, n),
        s('text', { class: 'tlabel tsub ' + st.cls, x: w / 2, y: 38, 'text-anchor': 'middle' }, sub));
      const place = () => { const p = pos.get(n); g.setAttribute('transform', `translate(${p.x.toFixed(1)} ${p.y.toFixed(1)})`); };
      place();
      g.addEventListener('pointerdown', (ev) => {
        if (ev.button !== 0) return;
        const start = toSvg(ev), p0 = { ...pos.get(n) };
        let moved = false;
        g.setPointerCapture(ev.pointerId);
        const move = (e) => {
          if (!moved && Math.hypot(e.clientX - ev.clientX, e.clientY - ev.clientY) < 4) return;
          moved = true; g.classList.add('dragging');
          const q = toSvg(e);
          pos.set(n, { x: Math.max(0, p0.x + q.x - start.x), y: Math.max(0, p0.y + q.y - start.y) });
          place(); placeEdges();
        };
        const up = () => {
          g.removeEventListener('pointermove', move); g.removeEventListener('pointerup', up); g.removeEventListener('pointercancel', up);
          g.classList.remove('dragging');
          if (moved) { persist(); fit(); }
        };
        g.addEventListener('pointermove', move); g.addEventListener('pointerup', up); g.addEventListener('pointercancel', up);
      });
      g.addEventListener('keydown', (ev) => {
        const d = { ArrowLeft: [-14, 0], ArrowRight: [14, 0], ArrowUp: [0, -14], ArrowDown: [0, 14] }[ev.key];
        if (!d) return;
        ev.preventDefault(); const p = pos.get(n); pos.set(n, { x: Math.max(0, p.x + d[0]), y: Math.max(0, p.y + d[1]) }); place(); placeEdges(); persist(); fit();
      });
      svg.append(g);
    }
    placeEdges(); fit();
    const reset = h('button', { type: 'button', class: 'small', onclick: () => { try { localStorage.removeItem('apm_tmap_pos'); } catch (e) { /* yok */ } render(); } }, 'Yerleşimi sıfırla');
    const lg = (cls, text) => h('span', { class: 'tm-key' }, h('i', { class: 'tm-sw ' + cls }), text);
    return h('div', { class: 'stack' }, h('div', { class: 'filters' }, since, src, reset),
      h('p', { class: 'muted small' }, 'Oklar çağıran → çağrılan yönündedir; kesikli kutular veritabanı/dış servis gibi izlenmeyen bağımlılıklardır, kırmızı oklar %5 üzeri hata oranını gösterir; kesikli oklar ajanın gözlemlediği TCP bağlantılarıdır (gecikme/hata bilgisi içermez). Kutuları sürükleyerek yerleştirebilirsiniz; yerleşim bu tarayıcıda saklanır.'),
      h('div', { class: 'tm-legend small' }, 'Çerçeve rengi: ', lg('ok', 'sağlıklı'), lg('warn', 'uyarı (hata var ya da p95 ≥ 800 ms)'), lg('danger', 'çok yavaş (p95 ≥ 2 sn)'), lg('bad', 'hata oranı ≥ %5'), lg('none', 'ölçüm yok')),
      h('div', { class: 'tmap-wrap' }, svg), traceSetup());
  }
  async function viewTrace() {
    await refreshHeader();
    const tabs = h('div', { class: 'seg', role: 'group', 'aria-label': 'İzleme sekmeleri' }, TRACE_TABS.map(([id, label]) => {
      const b = h('button', { type: 'button', 'aria-pressed': String(TRACE.tab === id) }, label);
      b.addEventListener('click', () => { TRACE.tab = id; render(); });
      return b;
    }));
    const body = await ({ list: traceList, services: traceServices, map: traceMap, logs: traceLogsTab }[TRACE.tab] || traceList)();
    return h('section', { class: 'stack' }, h('h2', null, 'Dağıtık izleme'),
      h('p', { class: 'muted small' }, 'Bir isteğin servisler arasında izlediği yolu span\'lar halinde görün: hangi servis ne kadar sürdü, nerede hata oluştu, hangi servis hangi bağımlılığı çağırıyor. OpenTelemetry (OTLP/HTTP) ile agent\'sız alınır.'),
      h('p', { class: 'muted small' }, 'APP_MON kendi API isteklerini de "appmon-server" servisi olarak kaydeder (hatalı ve yavaş istekler her zaman, diğerleri örneklenerek).'),
      tabs, body);
  }

  // ---- Asset kartı: bir asset'in tüm bilgisi tek sayfada (v3.15.0) ----
  const CARD = { tab: 'genel', win: 60 };
  const CARD_TABS = [['genel', 'Genel'], ['yazilim', 'Yazılım envanteri'], ['topoloji', 'Topoloji ve bağlantılar'], ['performans', 'Performans'], ['guvenlik', 'Güvenlik'], ['olaylar', 'Olaylar']];
  const cardId = () => Number(location.hash.slice(1).split('/')[1]) || 0;
  function openCard(id, tab) {
    CARD.tab = tab || 'genel';
    if (location.hash === '#kart/' + id) render(); else location.hash = 'kart/' + id;
  }
  const cardLink = (id, text, tab) => h('a', { href: '#kart/' + id, class: 'cardlink', onclick: (e) => { e.preventDefault(); openCard(id, tab); } }, text);
  const settled = (r) => (r && r.status === 'fulfilled' ? r.value : null);
  const SEV_CLS = { critical: 'down', warning: 'warning', info: 'info' };
  const SEV_TXT = { critical: 'Kritik', warning: 'Uyarı', info: 'Bilgi' };

  async function viewKart() {
    await refreshHeader();
    const id = cardId();
    const [assets, alerts] = await Promise.all([api('/api/assets'), api('/api/alerts?state=open')]);
    const a = assets.find((x) => x.id === id);
    if (!a) return h('section', { class: 'stack' }, h('p', { class: 'error' }, 'Asset bulunamadı (silinmiş olabilir).'), h('a', { href: '#assetler' }, '← Assetler'));
    const mine = alerts.filter((x) => x.asset_id === id);
    const lat = a.latest || {};
    const crit = mine.filter((x) => x.severity === 'critical').length;
    const ctx = { a, assets, mine };
    const head = h('div', { class: 'panel cardhead' },
      h('div', { class: 'row' }, h('span', { class: 'kl' }, kindIcon(a.kind, 40), h('h2', null, a.name)), pill(a.status, STATUS[a.status] || a.status)),
      h('p', { class: 'muted small' }, [KIND[a.kind] || a.kind, LANG[a.language] && a.language !== 'none' ? LANG[a.language] : null, a.group ? 'grup: ' + a.group : null, 'son veri: ' + ago(a.last_seen)].filter(Boolean).join(' · '),
        (a.tags || []).map((t) => h('span', { class: 'tagchip' }, ' ' + t))));
    const kp = [kpi('Açık alarm', String(mine.length), crit ? crit + ' kritik' : mine.length ? 'hepsi uyarı/bilgi' : 'temiz', null, crit ? 'bad' : mine.length ? 'warn' : '', () => { CARD.tab = 'olaylar'; render(); })];
    if (lat['host.cpu_pct'] != null) kp.push(kpi('CPU', fmt(lat['host.cpu_pct']) + '%', 'son 10 dk', null, lat['host.cpu_pct'] >= 90 ? 'bad' : lat['host.cpu_pct'] >= 75 ? 'warn' : '', () => { CARD.tab = 'performans'; render(); }));
    if (lat['host.mem_pct'] != null) kp.push(kpi('Bellek', fmt(lat['host.mem_pct']) + '%', 'son 10 dk', null, lat['host.mem_pct'] >= 90 ? 'bad' : lat['host.mem_pct'] >= 80 ? 'warn' : '', () => { CARD.tab = 'performans'; render(); }));
    if (lat['host.disk_pct'] != null) kp.push(kpi('En dolu disk', fmt(lat['host.disk_pct']) + '%', 'son 10 dk', null, lat['host.disk_pct'] >= 95 ? 'bad' : lat['host.disk_pct'] >= 85 ? 'warn' : '', () => { CARD.tab = 'performans'; render(); }));
    if (lat['probe.latency_ms'] != null) kp.push(kpi('Yanıt süresi', pms(lat['probe.latency_ms']), 'probe, son 10 dk', null, lat['probe.latency_ms'] >= 3000 ? 'bad' : lat['probe.latency_ms'] >= 1000 ? 'warn' : '', () => { CARD.tab = 'performans'; render(); }));
    const tabs = h('div', { class: 'seg', role: 'group', 'aria-label': 'Asset kartı sekmeleri' }, CARD_TABS.map(([tid, label]) => {
      const b = h('button', { type: 'button', 'aria-pressed': String(CARD.tab === tid) }, label);
      b.addEventListener('click', () => { CARD.tab = tid; render(); });
      return b;
    }));
    let body;
    try { body = await ({ genel: cardGenel, yazilim: cardYazilim, topoloji: cardTopoloji, performans: cardPerformans, guvenlik: cardGuvenlik, olaylar: cardOlaylar }[CARD.tab] || cardGenel)(ctx); }
    catch (e) { body = h('p', { class: 'error' }, 'Yüklenemedi: ' + e.message); }
    return h('section', { class: 'stack' }, h('div', { class: 'row' }, h('a', { href: '#assetler' }, '← Assetler')), head, h('div', { class: 'kpis cardk' }, kp), tabs, body);
  }

  // Genel: kısa değerlendirme, özet kutuları, tanım alanları, bağlı probe/senaryolar.
  async function cardGenel({ a, mine }) {
    const id = a.id;
    const [hi, imp, probes, scen, sla, inv, sev] = (await Promise.allSettled([api(`/api/assets/${id}/hostinfo`), api(`/api/assets/${id}/impact`), api('/api/probes'), api('/api/scenarios'), api('/api/sla'), api('/api/inventory'), api('/api/surface/events?state=open')])).map(settled);
    const myProbes = (probes || []).filter((p) => p.asset_id === id), myScen = (scen || []).filter((x) => x.asset_id === id);
    const slaRow = sla && sla.rows ? sla.rows.find((r) => r.asset_id === id) : null;
    const ia = inv && inv.assets ? inv.assets.find((x) => x.asset_id === id) : null;
    const drift = sev && sev.events ? sev.events.filter((e) => e.asset_id === id) : [];
    const impacted = imp ? imp.impacted : [], deps = imp ? imp.dependencies : [];
    const crit = mine.filter((x) => x.severity === 'critical').length;
    let ins;
    if (a.status === 'down' || a.status === 'offline') ins = { cls: 'down', head: STATUS[a.status], text: `Son veri ${ago(a.last_seen)}. ${impacted.length ? impacted.length + ' asset bu varlığa bağımlı; kesinti onları da etkileyebilir. ' : ''}Topoloji sekmesinden bağlantıları, Olaylar sekmesinden alarm geçmişini inceleyin.` };
    else if (crit) ins = { cls: 'down', head: 'Kritik alarm', text: `${crit} kritik alarm açık: ${mine.filter((x) => x.severity === 'critical').slice(0, 2).map((x) => x.rule).join(', ')}. Olaylar sekmesinde ayrıntı ve kök neden var.` };
    else if (ia && ia.critical) ins = { cls: 'down', head: 'Kritik zafiyet', text: `${ia.critical} kritik zafiyetli bağımlılık kullanılıyor; Yazılım envanteri sekmesinde düzeltme sürümleri var.` };
    else if (a.status === 'degraded' || mine.length) ins = { cls: 'warning', head: a.status === 'degraded' ? 'Kısmen erişilemiyor' : 'Açık alarm', text: `${mine.length} açık alarm var. Performans sekmesinde eğilime, Olaylar sekmesinde zaman çizelgesine bakın.` };
    else if (drift.length) ins = { cls: 'warning', head: 'Saldırı yüzeyi sapması', text: `${drift.length} gözden geçirilmemiş sapma var (yeni port, uç nokta vb.). Güvenlik sekmesine bakın.` };
    else if (ia && ia.eol_count) ins = { cls: 'warning', head: 'Destek dışı sürüm', text: `${ia.eol_count} servis/çalışma zamanı üretici desteği bitmiş sürümde.` };
    else ins = { cls: 'up', head: 'Sağlıklı', text: 'Açık alarm, kritik zafiyet veya saldırı yüzeyi sapması görünmüyor.' };
    const tile = (label, val, sub, tab, tone) => kpi(label, val, sub, null, tone, () => { CARD.tab = tab; render(); });
    const tiles = h('div', { class: 'kpis cardk' },
      tile('Bağımlılık', String(deps.length), deps.length ? deps.slice(0, 2).map((d) => d.name).join(', ') + (deps.length > 2 ? '…' : '') : 'bağlı olduğu asset yok', 'topoloji'),
      tile('Etkilenen', String(impacted.length), impacted.length ? 'düşerse etkilenir' : 'ona bağımlı asset yok', 'topoloji', impacted.length ? 'warn' : ''),
      tile('Dinleyen servis', ia ? String(ia.listeners.length) : '—', ia ? `${ia.listeners.filter((l) => l.scope === 'net').length} ağdan erişilebilir` : 'envanter yok (ajan v1.6.0+)', 'yazilim'),
      tile('Zafiyet', ia ? String(ia.vuln_total) : '—', ia ? `${ia.critical} kritik · ${ia.high} yüksek` : 'envanter yok', 'yazilim', ia && ia.critical ? 'bad' : ia && ia.high ? 'warn' : ''),
      tile('Saldırı yüzeyi', String(drift.length), 'açık sapma', 'guvenlik', drift.some((e) => e.severity === 'critical') ? 'bad' : drift.length ? 'warn' : ''),
      slaRow ? tile('Erişilebilirlik (SLA)', slaRow.availability_pct != null ? slaRow.availability_pct.toFixed(2) + '%' : '—', `hedef %${slaRow.target_pct}`, 'olaylar', slaRow.met === false ? 'bad' : '') : null);
    const hm = (hi && hi.metrics) || {};
    const rows = [['Tür', KIND[a.kind] || a.kind], ['Kaynak', a.collection === 'agent' ? 'Ajan' : a.collection === 'otel' ? 'OpenTelemetry' : a.collection || '—'], ['Grup', a.group || '—'],
      ['Son veri', a.last_seen ? new Date(a.last_seen).toLocaleString() + ' (' + ago(a.last_seen) + ')' : '—'],
      hi && hi.addrs && hi.addrs.length ? ['IP adresleri', hi.addrs.join(', ')] : null,
      hm['host.uptime_sec'] != null ? ['Açık kalma süresi', dur(hm['host.uptime_sec'])] : null,
      hm['host.load1'] != null ? ['Yük (1/5/15 dk)', [hm['host.load1'], hm['host.load5'], hm['host.load15']].map((v) => (v == null ? '—' : v.toFixed(2))).join(' / ')] : null,
      hm['host.mem_total_gb'] != null ? ['Bellek', `${fmt(hm['host.mem_used_gb'])} / ${fmt(hm['host.mem_total_gb'])} GB`] : null,
      hm['host.conns'] != null ? ['Kurulu bağlantı', String(hm['host.conns']) + (hm['host.conn_rtt_ms'] != null ? ` · ortalama RTT ${fmt(hm['host.conn_rtt_ms'])} ms` : '')] : null,
      hi && hi.ports && hi.ports.length ? ['Dinleyen portlar (SSH/ajan)', hi.ports.join(', ')] : null].filter(Boolean);
    const LBL = { os: 'İşletim sistemi', arch: 'Mimari', agent_version: 'Ajan sürümü', hostname: 'Bilgisayar adı', kernel: 'Çekirdek' };
    const labelRows = a.labels && typeof a.labels === 'object' ? Object.entries(a.labels).filter(([k]) => LBL[k.toLowerCase()]).map(([k, v]) => [LBL[k.toLowerCase()], String(v)]) : [];
    const mon = [...myProbes.map((p) => h('tr', null, h('td', null, pill('info', 'probe')), h('td', null, h('b', null, p.type), h('div', { class: 'mono small muted' }, p.target)),
      h('td', null, !p.enabled ? pill('unknown', 'kapalı') : p.last_error ? pill('down', 'hata') : pill('up', 'tamam')), h('td', { class: 'small' }, p.last_error || ('her ' + p.interval_sec + ' sn · ' + ago(p.last_run))))),
      ...myScen.map((x) => h('tr', null, h('td', null, pill('info', 'senaryo')), h('td', null, h('b', null, x.name)),
        h('td', null, !x.enabled ? pill('unknown', 'kapalı') : x.last_error ? pill('down', 'hata') : pill('up', 'tamam')), h('td', { class: 'small' }, x.last_error || ('her ' + x.interval_sec + ' sn'))))];
    return h('div', { class: 'stack' }, h('div', { class: 'panel timing-note' }, pill(ins.cls, ins.head), h('div', null, ins.text)), tiles,
      h('h3', null, 'Tanım'), h('div', { class: 'panel' }, h('table', { class: 'kv-table' }, h('tbody', null, [...rows, ...labelRows].map(([k, v]) => h('tr', null, h('th', { scope: 'row' }, k), h('td', null, v)))))),
      mon.length ? [h('h3', null, `İzleyiciler (${mon.length})`), table(['Tür', 'Hedef', 'Durum', 'Ayrıntı'], mon, '', PAGE)] : h('p', { class: 'muted small' }, 'Bu asset için probe veya senaryo tanımlı değil.'),
      h('div', { class: 'row' }, h('button', { type: 'button', onclick: () => assetDialog(a) }, 'Ayar penceresi (genel bakışta göster, disk ayrıntısı)')));
  }

  // Yazılım envanteri: envanter (port/sürüm/zafiyet) + ajanın süreç/uygulama listesi.
  async function cardYazilim({ a }) {
    const [inv, apps] = (await Promise.allSettled([api('/api/inventory'), api(`/api/assets/${a.id}/apps`)])).map(settled);
    const ia = inv && inv.assets ? inv.assets.find((x) => x.asset_id === a.id) : null;
    const adv = (inv && inv.advisory) || {};
    const msg = h('p', { class: 'msg', role: 'status' });
    const as = apps ? appsSection(apps) : null;
    return h('div', { class: 'stack' },
      ia ? [invDetail(ia, adv, msg), msg]
        : h('div', { class: 'panel' }, h('b', null, 'Bu asset için envanter yok. '), 'Ajan v1.6.0+ kurulu olmalı ve Ajanlar sayfasında bu makinenin veri ayarında "Saldırı yüzeyi envanteri" işaretli olmalı. Ajansız assetlerde (web sitesi, servis) envanter toplanmaz.'),
      as || null);
  }

  // Topoloji ve bağlantılar: doğrudan komşular, etkilenenler, gözlenen ağ bağlantıları.
  async function cardTopoloji({ a, assets }) {
    const [topo, imp, conns] = (await Promise.allSettled([api('/api/topology'), api(`/api/assets/${a.id}/impact`), api('/api/perf/conns')])).map(settled);
    const nodes = new Map(((topo && topo.nodes) || assets.map((x) => ({ id: x.id, name: x.name, kind: x.kind, status: x.status }))).map((n) => [n.id, n]));
    const edges = (topo && topo.edges) || [];
    const out = edges.filter((e) => e.source === a.id), inn = edges.filter((e) => e.target === a.id);
    const rootCause = topo && (topo.root_causes || []).includes(a.id);
    const kindTxt = (k) => (k === 'runs_on' ? 'üzerinde çalışır' : 'bağımlıdır');
    const box = (nid, k, dir) => {
      const n = nodes.get(nid) || { name: '#' + nid, status: 'unknown', kind: 'service' };
      const el = h('button', { type: 'button', class: 'nbox st-' + n.status, title: 'Asset kartını aç' }, h('span', { class: 'kl' }, kindIcon(n.kind, 26), h('b', null, n.name)), h('span', { class: 'muted small' }, (dir === 'in' ? 'bana ' : 'ben ') + kindTxt(k) + ' · ' + (STATUS[n.status] || n.status)));
      el.addEventListener('click', () => openCard(nid));
      return el;
    };
    const center = h('div', { class: 'nbox ncenter st-' + a.status }, h('span', { class: 'kl' }, kindIcon(a.kind, 34), h('b', null, a.name)), pill(a.status, STATUS[a.status] || a.status), rootCause ? pill('down', 'olası kök neden') : null);
    const col = (title, list) => h('div', { class: 'ncol' }, h('div', { class: 'muted small' }, title), list.length ? list : h('div', { class: 'muted small' }, '—'));
    const map = h('div', { class: 'nhood panel' },
      col(`Bana bağımlı olanlar (${inn.length})`, inn.map((e) => box(e.source, e.kind, 'in'))), h('div', { class: 'narrow', 'aria-hidden': 'true' }, '→'),
      center, h('div', { class: 'narrow', 'aria-hidden': 'true' }, '→'),
      col(`Benim bağımlı olduklarım (${out.length})`, out.map((e) => box(e.target, e.kind, 'out'))));
    const lists = [];
    if (imp && imp.impacted.length) lists.push(h('div', null, h('h3', null, `Düşerse etkilenenler (${imp.impacted.length})`), h('p', { class: 'muted small' }, 'Doğrudan ve dolaylı; derinlik 1 = doğrudan bağımlı.'),
      table(['Asset', 'Durum', 'Derinlik'], imp.impacted.map((n) => h('tr', null, h('td', null, cardLink(n.id, n.name)), h('td', null, pill(n.status, STATUS[n.status] || n.status)), h('td', { class: 'mono' }, String(n.depth)))), '', PAGE)));
    if (imp && imp.dependencies.length) lists.push(h('div', null, h('h3', null, `Bağımlı olduğu zincir (${imp.dependencies.length})`),
      table(['Asset', 'Durum', 'Derinlik'], imp.dependencies.map((n) => h('tr', null, h('td', null, cardLink(n.id, n.name)), h('td', null, pill(n.status, STATUS[n.status] || n.status)), h('td', { class: 'mono' }, String(n.depth)))), '', PAGE)));
    const links = conns && conns.links ? conns.links.filter((l) => l.source_id === a.id || l.target_id === a.id) : [];
    const unk = conns && conns.unknown ? conns.unknown.filter((u) => u.asset === a.name) : [];
    const host = conns && conns.hosts ? conns.hosts.find((x) => x.asset_id === a.id) : null;
    const crow = links.map((l) => { const outgoing = l.source_id === a.id; const peer = outgoing ? l.target : l.source, pid = outgoing ? l.target_id : l.source_id;
      return h('tr', null, h('td', null, outgoing ? pill('info', 'giden →') : pill('up', '← gelen')), h('td', null, pid ? cardLink(pid, peer) : peer), h('td', { class: 'mono' }, String(l.port)), h('td', { class: 'mono' }, String(l.conns)), h('td', { class: 'mono' }, l.rtt_ms != null ? fmt(l.rtt_ms) + ' ms' : '—'),
        h('td', null, l.edge_exists ? pill('up', 'tanımlı') : pill('warning', 'tanımsız'), !l.edge_exists ? h('div', { class: 'muted small' }, 'gözlendi ama topolojide yok') : null)); });
    const urow = unk.map((u) => h('tr', null, h('td', null, pill('info', 'giden →')), h('td', { class: 'mono' }, u.peer), h('td', { class: 'mono' }, String(u.port)), h('td', { class: 'mono' }, String(u.conns)), h('td', { class: 'mono' }, u.rtt_ms != null ? fmt(u.rtt_ms) + ' ms' : '—'), h('td', null, pill('unknown', 'asset değil'))));
    return h('div', { class: 'stack' },
      h('p', { class: 'muted small' }, 'Kutuya tıklayınca o asset\'in kartı açılır. Bağlantıları düzenlemek için Topoloji sayfasını kullanın.'), map, ...lists,
      h('h3', null, 'Gözlenen ağ bağlantıları'),
      host ? h('p', { class: 'muted small' }, `Ajanın görüntüsü: ${host.in || 0} gelen, ${host.out || 0} giden kurulu bağlantı · ${ago(host.updated_at)}`) : null,
      crow.length || urow.length ? table(['Yön', 'Karşı taraf', 'Port', 'Bağlantı sayısı', 'RTT', 'Topoloji'], [...crow, ...urow], '', PAGE)
        : h('p', { class: 'muted' }, 'Bu asset için gözlenen bağlantı yok (ajan bağlantı verisi göndermiyor ya da henüz bağlantı görülmedi).'),
      h('div', { class: 'row' }, h('button', { type: 'button', onclick: () => goTo('topoloji') }, 'Topoloji sayfasını aç')));
  }

  // Performans: metrik eğilimleri (dönem seçilebilir), uygulama istek/hata/gecikme, yavaş sorgular.
  async function cardPerformans({ a }) {
    const wins = [[60, '1 saat'], [360, '6 saat'], [1440, '24 saat']];
    const [m, apps, qs] = (await Promise.allSettled([api(`/api/assets/${a.id}/metrics?minutes=${CARD.win}`), api('/api/perf/apps?window=1h'), api('/api/perf/queries')])).map(settled);
    const seg = h('div', { class: 'seg', role: 'group', 'aria-label': 'Dönem' }, wins.map(([v, l]) => { const b = h('button', { type: 'button', 'aria-pressed': String(CARD.win === v) }, l); b.addEventListener('click', () => { CARD.win = v; render(); }); return b; }));
    const data = m || {};
    const names = Object.keys(data).filter((n) => !/^host\.disk_(used|total)_gb\./.test(n) && data[n].length);
    const GROUPS = [['Sunucu', /^host\./], ['Ağ', /^net\./], ['Probe', /^probe\./], ['Veritabanı', /^db\./], ['Senaryo', /^scenario\./], ['Günlük kayıtları', /^log\./]];
    const used = new Set();
    const grp = GROUPS.map(([title, re]) => { const ns = names.filter((n) => re.test(n)); ns.forEach((n) => used.add(n)); return [title, ns]; });
    const rest = names.filter((n) => !used.has(n)); if (rest.length) grp.push(['Diğer', rest]);
    const sparkBox = (n) => { const pts = data[n]; const vs = pts.map((p) => p[1]); const last = vs[vs.length - 1];
      return h('div', { class: 'spark' }, h('span', { class: 'mono small muted' }, n), pts.length > 1 ? spark(pts) : h('span', { class: 'muted small' }, 'tek örnek'),
        h('span', { class: 'small' }, h('b', { class: 'mono' }, fmt(last)), h('span', { class: 'muted' }, `  en az ${fmt(Math.min(...vs))} · en çok ${fmt(Math.max(...vs))}`))); };
    const myApps = apps && apps.apps ? apps.apps.filter((x) => x.asset_id === a.id) : [];
    const appRows = myApps.map((x) => { const ins = x.err_pct >= 5 ? { cls: 'down', head: 'Hata yüksek', text: `%${x.err_pct.toFixed(1)} hata; son hatalı isteklere Olaylar sekmesinden bakın.` } : x.p95_ms >= 2000 ? { cls: 'down', head: 'Çok yavaş', text: 'p95 2 sn üzerinde.' } : x.err_pct >= 1 || x.p95_ms >= 800 ? { cls: 'warning', head: 'Dikkat', text: x.err_pct >= 1 ? 'Hata oranı %1 üzerinde.' : 'p95 800 ms üzerinde.' } : { cls: 'up', head: 'Sağlıklı', text: 'Hata ve gecikme normal.' };
      return h('tr', null, h('td', null, h('b', null, x.app)), h('td', { class: 'mono' }, fmt(x.req_per_min)), h('td', { class: 'mono' }, x.err_pct > 0 ? pill(x.err_pct >= 5 ? 'down' : 'warning', ppct(x.err_pct)) : '0%'), h('td', { class: 'mono' }, pms(x.p50_ms)), h('td', { class: 'mono' }, pms(x.p95_ms)), insightCell(ins)); });
    const epBox = h('div');
    const epBtn = myApps.length ? h('button', { type: 'button', onclick: async () => {
      try { const r = await api(`/api/perf/endpoints?asset=${a.id}&window=1h`);
        const rows = (r.endpoints || []).slice(0, 15).map((e) => h('tr', null, h('td', { class: 'mono small' }, e.endpoint || e.path || e.name || '—'), h('td', { class: 'mono' }, String(e.reqs)), h('td', { class: 'mono' }, e.err5xx ? pill('down', String(e.err5xx)) : '0'), h('td', { class: 'mono' }, pms(e.p95_ms))));
        epBox.replaceChildren(table(['Uç nokta', 'İstek', '5xx', 'p95'], rows, 'Uç nokta verisi yok.', PAGE)); epBtn.hidden = true; } catch (e) { epBox.textContent = e.message; } } }, 'En yoğun uç noktaları göster') : null;
    const myQ = qs && qs.queries ? qs.queries.filter((q) => q.asset === a.name).sort((x, y) => y.total_ms - x.total_ms).slice(0, 10) : [];
    const qRows = myQ.map((q) => h('tr', null, h('td', { class: 'mono small' }, (q.query || '').slice(0, 110)), h('td', { class: 'mono' }, String(q.calls)), h('td', { class: 'mono' }, pms(q.mean_ms)), h('td', { class: 'mono' }, pms(q.max_ms)), h('td', { class: 'mono' }, pms(q.total_ms))));
    return h('div', { class: 'stack' }, h('div', { class: 'row' }, h('h3', null, 'Metrik eğilimleri'), seg,
      names.some((n) => n.startsWith('host.')) ? h('button', { type: 'button', onclick: () => diskDialog(a.id, a.name) }, 'Disk ve sunucu ayrıntısı') : null),
      grp.filter(([, ns]) => ns.length).map(([title, ns]) => h('div', null, h('h4', null, title), h('div', { class: 'panel sparks' }, ns.map(sparkBox)))),
      names.length ? null : h('p', { class: 'muted' }, 'Seçilen dönemde bu asset için metrik yok.'),
      appRows.length ? [h('h3', null, 'Uygulama performansı (son 1 saat)'), table(['Uygulama', 'İstek/dk', 'Hata oranı', 'p50', 'p95', 'Yorum'], appRows, '', PAGE), epBtn, epBox] : null,
      qRows.length ? [h('h3', null, 'En çok zaman harcayan sorgular'), table(['Sorgu', 'Çağrı', 'Ortalama', 'En yavaş', 'Toplam'], qRows, '', PAGE)] : null);
  }

  // Güvenlik: sapmalar, referans (baseline) ve ağdan erişilebilir riskli portlar, sertifika, zafiyet özeti.
  async function cardGuvenlik({ a }) {
    const [ev, base, inv, m] = (await Promise.allSettled([api('/api/surface/events?state=all'), api('/api/surface/baseline'), api('/api/inventory'), api(`/api/assets/${a.id}/metrics?minutes=60`)])).map(settled);
    const events = ev && ev.events ? ev.events.filter((e) => e.asset_id === a.id) : [];
    const open = events.filter((e) => !e.reviewed);
    const bl = base ? (Array.isArray(base) ? base : base.assets || []).find((x) => x.asset_id === a.id) : null;
    const ia = inv && inv.assets ? inv.assets.find((x) => x.asset_id === a.id) : null;
    const exposed = ia ? ia.listeners.filter((l) => l.scope === 'net') : [];
    const risky = exposed.filter((l) => RISKY_PORTS.has(l.port));
    const cert = m && m['probe.cert_days_left'] && m['probe.cert_days_left'].length ? m['probe.cert_days_left'][m['probe.cert_days_left'].length - 1][1] : null;
    let ins;
    if (open.some((e) => e.severity === 'critical') || risky.length || (ia && ia.critical)) ins = { cls: 'down', head: 'Acil inceleme', text: [open.filter((e) => e.severity === 'critical').length ? `${open.filter((e) => e.severity === 'critical').length} kritik sapma` : null, risky.length ? `riskli port açık (${risky.map((l) => l.port).join(', ')})` : null, ia && ia.critical ? `${ia.critical} kritik zafiyet` : null].filter(Boolean).join(', ') + '.' };
    else if (open.length || (ia && (ia.eol_count || ia.high)) || (cert != null && cert < 14)) ins = { cls: 'warning', head: 'Dikkat', text: [open.length ? `${open.length} açık sapma` : null, ia && ia.eol_count ? `${ia.eol_count} destek dışı sürüm` : null, ia && ia.high ? `${ia.high} yüksek zafiyet` : null, cert != null && cert < 14 ? `sertifikanın bitimine ${Math.floor(cert)} gün` : null].filter(Boolean).join(', ') + '.' };
    else ins = { cls: 'up', head: 'Temiz', text: ia || bl ? 'Açık sapma, riskli port veya kritik zafiyet görünmüyor.' : 'Henüz referans veya envanter verisi yok; ilk gözlemler referans olur.' };
    const evRows = events.slice(0, 50).map((e) => h('tr', null, h('td', null, pill(SEV_CLS[e.severity] || 'info', SEV_TXT[e.severity] || e.severity)), h('td', null, SURF_KIND[e.kind] || e.kind), h('td', { class: 'mono small' }, e.item), h('td', { class: 'small' }, SURF_CHANGE[e.change] || e.change, e.detail ? h('div', { class: 'muted' }, e.detail) : null), h('td', { class: 'small' }, ago(e.at)), h('td', null, e.reviewed ? pill('up', 'gözden geçirildi') : pill('warning', 'Açık'))));
    const blRows = bl ? Object.entries(bl).filter(([k, v]) => k !== 'asset' && k !== 'asset_id' && (Array.isArray(v) ? v.length : v)).map(([k, v]) => h('tr', null, h('td', null, SURF_KIND[k.replace(/s$/, '')] || k), h('td', { class: 'mono small' }, Array.isArray(v) ? v.join(', ') : String(v)))) : [];
    return h('div', { class: 'stack' }, h('div', { class: 'panel timing-note' }, pill(ins.cls, ins.head), h('div', null, ins.text)),
      h('div', { class: 'kpis cardk' }, kpi('Açık sapma', String(open.length), 'gözden geçirilmemiş', null, open.some((e) => e.severity === 'critical') ? 'bad' : open.length ? 'warn' : ''),
        kpi('Ağdan erişilebilir port', ia ? String(exposed.length) : '—', ia ? (risky.length ? risky.length + ' riskli' : 'riskli port yok') : 'envanter yok', null, risky.length ? 'bad' : ''),
        kpi('Zafiyet', ia ? String(ia.vuln_total) : '—', ia ? `${ia.critical} kritik · ${ia.high} yüksek` : 'envanter yok', null, ia && ia.critical ? 'bad' : ia && ia.high ? 'warn' : '', () => { CARD.tab = 'yazilim'; render(); }),
        cert != null ? kpi('Sertifika', Math.floor(cert) + ' gün', 'kalan süre', null, cert < 3 ? 'bad' : cert < 14 ? 'warn' : '') : null),
      exposed.length ? [h('h3', null, 'Ağdan erişilebilen servisler'), table(['Port', 'Süreç', 'Sürüm', 'Destek'], exposed.map((l) => h('tr', null, h('td', { class: 'mono' }, String(l.port), RISKY_PORTS.has(l.port) ? [' ', pill('down', 'riskli')] : null, KNOWN_PORTS[l.port] ? h('div', { class: 'muted small' }, KNOWN_PORTS[l.port]) : null), h('td', null, l.proc), h('td', { class: 'mono small' }, l.runtime ? (RT_LABEL[l.runtime] || l.runtime) + (l.version && l.version !== 'unknown' ? ' ' + l.version : '') : '—'), h('td', null, eolPill(l.eol)))), '', PAGE)] : null,
      h('h3', null, `Sapmalar (${events.length})`), events.length ? table(['Önem', 'Tür', 'Öğe', 'Değişim', 'Zaman', 'Durum'], evRows, '', PAGE) : h('p', { class: 'muted' }, 'Bu asset için sapma kaydı yok.'),
      blRows.length ? [h('h3', null, 'Öğrenilmiş referans'), table(['Tür', 'Öğeler'], blRows, '', PAGE)] : null,
      h('div', { class: 'row' }, h('button', { type: 'button', onclick: () => goTo('yuzey') }, 'Saldırı yüzeyi sayfasını aç')));
  }

  // Olaylar: alarm geçmişi, sapmalar, senaryo değişiklikleri ve günlükler tek zaman çizelgesinde.
  async function cardOlaylar({ a }) {
    const [open, resolved, ev, scen, apps] = (await Promise.allSettled([api('/api/alerts?state=open'), api('/api/alerts?state=resolved'), api('/api/surface/events?state=all'), api('/api/scenarios'), api('/api/perf/apps?window=1h')])).map(settled);
    const items = [];
    for (const x of (open || []).filter((q) => q.asset_id === a.id)) items.push({ t: x.opened_at, kind: 'alarm', sev: x.severity, title: x.rule, text: `${x.metric} ${x.comparator} ${x.threshold} (değer ${fmt(x.value)})`, state: x.ack_by ? 'onaylandı: ' + x.ack_by : 'Açık', cls: SEV_CLS[x.severity] || 'info', id: x.id });
    for (const x of (resolved || []).filter((q) => q.asset_id === a.id).slice(0, 40)) items.push({ t: x.opened_at, kind: 'alarm', sev: x.severity, title: x.rule, text: `${x.metric} ${x.comparator} ${x.threshold}`, state: 'çözüldü ' + ago(x.resolved_at), cls: 'up' });
    for (const x of ((ev && ev.events) || []).filter((q) => q.asset_id === a.id).slice(0, 40)) items.push({ t: x.at, kind: 'Sapma kaydı', sev: x.severity, title: (SURF_KIND[x.kind] || x.kind) + ': ' + x.item, text: (SURF_CHANGE[x.change] || x.change) + (x.detail ? ' — ' + x.detail : ''), state: x.reviewed ? 'gözden geçirildi' : 'Açık', cls: SEV_CLS[x.severity] || 'info' });
    for (const x of (scen || []).filter((q) => q.asset_id === a.id)) {
      if (x.last_error) items.push({ t: new Date().toISOString(), kind: 'senaryo', title: x.name, text: x.last_error, state: 'son çalıştırma başarısız', cls: 'down' });
      if (x.last_changed_at) items.push({ t: x.last_changed_at, kind: 'senaryo', title: x.name, text: 'Sayfa yapısı değişti', state: '', cls: 'info' });
    }
    // Günlükler: bu asset'in uygulama/servis adlarıyla eşleşenler (günlük saklama açıksa).
    const svc = [...new Set(((apps && apps.apps) || []).filter((x) => x.asset_id === a.id).map((x) => x.app).concat([a.name]))].slice(0, 4);
    const logs = (await Promise.allSettled(svc.map((sv) => api('/api/logs?limit=30&level=warn&service=' + encodeURIComponent(sv))))).map(settled);
    let logOn = null;
    for (const r of logs) if (r) { logOn = r.enabled; for (const l of r.logs || []) items.push({ t: l.time, kind: 'Günlük kayıtları', title: l.service + ' · ' + l.level, text: l.message, state: '', cls: l.level === 'error' || l.level === 'fatal' ? 'down' : 'warning' }); }
    items.sort((x, y) => new Date(y.t) - new Date(x.t));
    const KINDS = ['hepsi', 'alarm', 'Sapma kaydı', 'senaryo', 'Günlük kayıtları'];
    const st = CARDF; const rows = () => items.filter((i) => st.k === 'hepsi' || i.kind === st.k).slice(0, 200);
    const box = h('div');
    const draw = () => box.replaceChildren(table(['Zaman', 'Tür', 'Olay', 'Ayrıntı', 'Durum'], rows().map((i) => h('tr', null, h('td', { class: 'small' }, h('div', null, ago(i.t)), h('div', { class: 'muted' }, new Date(i.t).toLocaleString())), h('td', null, pill(i.cls, i.kind)), h('td', null, h('b', null, i.title)), h('td', { class: 'small' }, (i.text || '').slice(0, 220)), h('td', { class: 'small' }, i.state))), 'Bu filtrede olay yok.', PAGE));
    const seg = h('div', { class: 'seg', role: 'group', 'aria-label': 'Olay türü' }, KINDS.map((k) => { const b = h('button', { type: 'button', 'aria-pressed': String(st.k === k) }, k === 'hepsi' ? `Hepsi (${items.length})` : `${k[0].toUpperCase() + k.slice(1)} (${items.filter((i) => i.kind === k).length})`); b.addEventListener('click', () => { st.k = k; for (const x of seg.children) x.setAttribute('aria-pressed', String(x === b)); draw(); }); return b; }));
    draw();
    return h('div', { class: 'stack' }, h('p', { class: 'muted small' }, 'Alarmlar, saldırı yüzeyi sapmaları, senaryo olayları ve (günlük saklama açıksa) uyarı/hata günlükleri zaman sırasıyla.' + (logOn === false ? ' Günlük saklama kapalı; günlükler görünmez.' : '')), seg, box,
      h('div', { class: 'row' }, h('button', { type: 'button', onclick: () => goTo('alarmlar') }, 'Alarmlar sayfası'), h('button', { type: 'button', onclick: () => goTo('trace') }, 'Dağıtık izleme')));
  }
  const CARDF = { k: 'hepsi' };

  const RENDER = { kart: viewKart, trace: viewTrace, raporlar: viewRaporlar, performans: viewPerformans, yuzey: viewYuzey, kanit: viewKanit, topoloji: viewTopoloji, genel: viewGenel, assetler: viewAssetler, probelar: viewProbelar, senaryolar: viewSenaryolar, alarmlar: viewAlarmlar, bildirimler: viewBildirimler, lisans: viewLisans, denetim: viewDenetim, ayarlar: viewAyarlar, yardim: viewYardim, guncelleme: viewGuncelleme, ajanlar: viewAjanlar, ag: viewAg, sla: viewSla, kullanicilar: viewKullanicilar, kiracilar: viewKiracilar, kasa: viewKasa };
  const AUTO = new Set(['genel', 'assetler', 'topoloji']);

  function current() {
    const v = location.hash.slice(1).split('/')[0];
    return RENDER[v] ? v : 'genel';
  }

  let rendering = false;
  async function render() {
    if (rendering) return;
    rendering = true;
    const v = current();
    for (const a of document.querySelectorAll('#nav a')) {
      if (a.dataset.v === v) a.setAttribute('aria-current', 'page');
      else a.removeAttribute('aria-current');
    }
    for (const g of document.querySelectorAll('#nav .navg')) g.classList.toggle('cur', g.dataset.ids.split(',').includes(v));
    try {
      if ((ADMIN_ONLY.includes(v) && ME.role !== 'admin') || ((v === 'kiracilar' || v === 'guncelleme') && !ME.platform)) { $('#view').replaceChildren(h('p', { class: 'error' }, 'Bu sayfa için yönetici yetkisi gerekir (rolünüz: ' + (ROLE_LABEL[ME.role] || ME.role) + ').')); return; }
      const node = await RENDER[v]();
      document.body.classList.toggle('tv', tv169 && v === 'genel');
      if (v === current()) { $('#view').dataset.page = v; $('#view').replaceChildren(node); fitTv(); }
    } catch (e) {
      if (key) $('#view').replaceChildren(h('p', { class: 'error' }, 'Yüklenemedi: ' + e.message));
    } finally { rendering = false; }
  }

  // ---- Oturum ----
  async function showApp() {
    $('#login').hidden = true; $('#app').hidden = false;
    await loadMe();
    buildNav(); render(); refreshBell();
  }
  function logout() {
    if (key) fetch('/api/logout', { method: 'POST', headers: { 'X-API-Key': key } }).catch(() => {});
    key = '';
    try { localStorage.removeItem('apm_key'); } catch (e) { /* yoksay */ }
    ME = { label: '', role: 'admin' }; document.body.removeAttribute('data-role'); $('#role-badge').hidden = true;
    $('#app').hidden = true; $('#login').hidden = false;
  }

  // Giriş: kullanıcı adı + parola (hesaplar/LDAP) veya API anahtarı.
  let loginMode = 'key';
  function setLoginMode(m) {
    loginMode = m;
    $('#login-user-box').hidden = m !== 'user'; $('#login-key-box').hidden = m !== 'key';
    $('#login-toggle').textContent = m === 'user' ? 'API anahtarı ile giriş' : 'Kullanıcı adı ile giriş';
  }
  $('#login-toggle').addEventListener('click', () => setLoginMode(loginMode === 'user' ? 'key' : 'user'));
  fetch('/api/auth/config').then((r) => r.json()).then((c) => { if (c.users) setLoginMode('user'); }).catch(() => {});
  setLoginMode('key');
  $('#login-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const err = $('#login-error');
    err.hidden = true;
    try {
      if (loginMode === 'user') {
        const res = await fetch('/api/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username: $('#login-user').value.trim(), password: $('#login-pass').value }) });
        const d = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(d.error || 'Giriş başarısız');
        key = d.key; $('#login-pass').value = '';
      } else { key = $('#login-key').value.trim(); await api('/api/overview'); }
      try { localStorage.setItem('apm_key', key); } catch (x) { /* yoksay */ }
      $('#login-key').value = '';
      showApp();
    } catch (ex) { key = ''; err.textContent = loginMode === 'user' ? ex.message : 'Anahtar geçersiz veya sunucuya ulaşılamıyor.'; err.hidden = false; }
  });
  $('#logout').addEventListener('click', logout);
  window.addEventListener('hashchange', () => { hideTip(); if (recCleanup) recCleanup(); selectedAsset = pendingAsset; pendingAsset = null; selectedNode = null; render(); });
  let autoTick = 0;
  setInterval(() => {
    // Asset sayısı arttıkça yenileme seyrekleşir: ≤100 asset 10 sn, ≤300 asset 20 sn, daha çoğunda 30 sn
    const every = (window.ASSET_N || 0) > 300 ? 3 : (window.ASSET_N || 0) > 100 ? 2 : 1;
    if (++autoTick % every) return;
    const typing = document.activeElement && /^(INPUT|SELECT|TEXTAREA)$/.test(document.activeElement.tagName);
    if (key && !$('#app').hidden && AUTO.has(current()) && !(current() === 'topoloji' && TOPO.paused) && !typing && !document.querySelector('.editing-row, .node.dragging, .thumb.dragging, .dw-editing')) render();
  }, 10000);

  // ---- Üst çubuk araçları: uyarı zili, sayfada arama, yazdırma ----
  const bell = $('#bell'), pop = $('#bell-pop');
  async function refreshBell() {
    if (!key || $('#app').hidden) return;
    try {
      const o = await api('/api/overview');
      const n = $('#bell-n'); n.textContent = o.open_alerts > 99 ? '99+' : String(o.open_alerts); n.hidden = !o.open_alerts;
      bell.classList.toggle('crit', o.critical > 0);
      bell.setAttribute('aria-label', o.open_alerts ? `${o.open_alerts} açık uyarı` : 'Açık uyarı yok');
    } catch (e) { /* sessizce geç */ }
  }
  async function toggleBell(force) {
    const open = force == null ? pop.hidden : force;
    if (!open) { pop.hidden = true; bell.setAttribute('aria-expanded', 'false'); return; }
    pop.hidden = false; bell.setAttribute('aria-expanded', 'true');
    pop.replaceChildren(h('p', { class: 'muted small pad' }, 'Yükleniyor…'));
    try {
      const list = await api('/api/alerts?state=open');
      pop.replaceChildren(h('div', { class: 'pop-head' }, h('b', null, 'Açık uyarılar'), h('span', { class: 'muted small' }, list.length + ' alarm')),
        list.length ? h('ul', { class: 'pop-list' }, list.slice(0, 8).map((a) => h('li', null, pill(a.severity, SEV[a.severity]), h('div', null, h('b', null, a.rule), h('div', { class: 'muted small' }, `${a.asset} · ${ago(a.opened_at)}`))))) : h('p', { class: 'muted small pad' }, 'Açık uyarı yok. Her şey yolunda.'),
        list.length ? h('button', { type: 'button', class: 'pop-clear', onclick: async () => {
          if (!window.confirm(`${list.length} açık alarm temizlensin mi?\n(Koşul hâlâ sürüyorsa alarm yeniden açılır.)`)) return;
          try { await api('/api/alerts/clear', { method: 'POST', body: {} }); await refreshBell(); toggleBell(false); render(); } catch (e) { alert(e.message); }
        } }, 'Alarmları temizle') : null,
        h('a', { class: 'pop-foot', href: '#alarmlar', onclick: () => toggleBell(false) }, list.length > 8 ? `Tümünü gör (+${list.length - 8})` : 'Alarmlar sayfasına git'));
    } catch (e) { pop.replaceChildren(h('p', { class: 'error small pad' }, e.message)); }
  }
  bell.addEventListener('click', (e) => { e.stopPropagation(); toggleBell(); });
  document.addEventListener('click', (e) => { if (!pop.hidden && !pop.contains(e.target)) toggleBell(false); });
  setInterval(refreshBell, 15000);

  // Sayfa içi arama: görünür metinde eşleşenleri işaretler (yalnızca #view veya açık rapor önizlemesi içinde).
  const fbar = $('#findbar'), finput = $('#find-input'), fcount = $('#find-count');
  let marks = [], mi = -1;
  function clearMarks() {
    for (const m of document.querySelectorAll('mark.find')) { const p = m.parentNode; m.replaceWith(document.createTextNode(m.textContent)); p.normalize(); }
    marks = []; mi = -1;
  }
  function doFind() {
    clearMarks();
    const q = finput.value.trim().toLowerCase();
    if (q.length < 2) { fcount.textContent = ''; return; }
    const root = $('#report-modal') && !$('#report-modal').hidden ? $('#report-body') : $('#view');
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, { acceptNode: (n) => (n.parentElement && !/^(SCRIPT|STYLE|TEXTAREA|OPTION|MARK)$/.test(n.parentElement.tagName) && n.nodeValue.toLowerCase().includes(q) && n.parentElement.offsetParent !== null ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT) });
    const nodes = []; while (walker.nextNode()) nodes.push(walker.currentNode);
    for (const n of nodes) {
      const txt = n.nodeValue, low = txt.toLowerCase(), frag = document.createDocumentFragment(); let i = 0, at;
      while ((at = low.indexOf(q, i)) !== -1) {
        if (at > i) frag.append(txt.slice(i, at));
        const m = h('mark', { class: 'find' }, txt.slice(at, at + q.length)); frag.append(m); marks.push(m); i = at + q.length;
      }
      frag.append(txt.slice(i)); n.replaceWith(frag);
    }
    fcount.textContent = marks.length ? '' : 'Sonuç yok';
    if (marks.length) stepFind(1);
  }
  function stepFind(d) {
    if (!marks.length) return;
    if (marks[mi]) marks[mi].classList.remove('cur');
    mi = (mi + d + marks.length) % marks.length;
    marks[mi].classList.add('cur'); marks[mi].scrollIntoView({ block: 'center', behavior: 'smooth' });
    fcount.textContent = `${mi + 1} / ${marks.length}`;
  }
  function toggleFind(on) {
    fbar.hidden = !on;
    if (on) { finput.focus(); finput.select(); } else { clearMarks(); finput.value = ''; fcount.textContent = ''; }
  }
  $('#find-btn').addEventListener('click', () => toggleFind(fbar.hidden));
  $('#find-close').addEventListener('click', () => toggleFind(false));
  $('#find-next').addEventListener('click', () => stepFind(1));
  $('#find-prev').addEventListener('click', () => stepFind(-1));
  let ft = 0; finput.addEventListener('input', () => { clearTimeout(ft); ft = setTimeout(doFind, 200); });
  finput.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); stepFind(e.shiftKey ? -1 : 1); } if (e.key === 'Escape') toggleFind(false); });
  document.addEventListener('keydown', (e) => {
    const typing = document.activeElement && /^(INPUT|SELECT|TEXTAREA)$/.test(document.activeElement.tagName);
    if (e.key === '/' && !typing && !$('#app').hidden) { e.preventDefault(); toggleFind(true); }
    if (e.key === 'Escape') toggleBell(false);
  });
  // Otomatik yenileme sayfayı yeniden çizince işaretler silinir; aramayı yeniden uygula.
  new MutationObserver(() => { if (!fbar.hidden && finput.value.trim().length >= 2 && !document.querySelector('mark.find')) { clearTimeout(ft); ft = setTimeout(doFind, 150); } }).observe($('#view'), { childList: true });
  $('#print-btn').addEventListener('click', () => { clearMarks(); window.print(); });

  // Rapor modülü (reports.js) için paylaşılan yardımcılar
  window.APM = { h, s, api, kpi, availChart, alertsChart, rankBars, pill, fmt, ago, table, kindIcon, KIND, STATUS, SEV, LANG, clearMarks, toggleFind, doFind };

  if (key) { api('/api/overview').then(showApp).catch(() => { $('#login').hidden = false; }); } else { $('#login').hidden = false; }
})();
