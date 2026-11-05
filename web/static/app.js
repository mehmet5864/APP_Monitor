(() => {
  'use strict';

  // ---- Yardımcılar ----
  const $ = (sel, root = document) => root.querySelector(sel);
  const LANG = { none: '—', dotnet: '.NET', java: 'Java', python: 'Python', go: 'Go' };
  const STATUS = { up: 'Çalışıyor', down: 'Erişilemiyor', offline: 'Çevrimdışı', unknown: 'Bilinmiyor' };
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

  function table(headers, rows, emptyText) {
    const body = rows.length
      ? rows
      : [h('tr', null, h('td', { class: 'empty', colspan: headers.length }, emptyText))];
    return h('div', { class: 'tablewrap' }, h('table', null,
      h('thead', null, h('tr', null, headers.map((t) => h('th', null, t)))),
      h('tbody', null, body)));
  }

  // Liste filtreleri: durum modül düzeyinde tutulur (10 sn'lik otomatik yenilemede kaybolmaz).
  const FILTERS = { assetler: { q: '', status: '', kind: '', coll: '' }, probelar: { q: '', type: '', res: '' } };
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
    ['genel', 'Genel bakış'], ['assetler', 'Assetler'], ['topoloji', 'Topoloji'], ['probelar', 'Probe\'lar'], ['senaryolar', 'Senaryolar'],
    ['alarmlar', 'Alarmlar'], ['lisans', 'Lisans ve anahtarlar'], ['denetim', 'Denetim'], ['ayarlar', 'Ayarlar'],
  ];

  // ---- Yetki rolleri ----
  const ROLE_LABEL = { admin: 'Yönetici', operator: 'İşletmeci', viewer: 'İzleyici', agent: 'Agent' };
  const ADMIN_ONLY = ['lisans', 'denetim'];
  let ME = { label: '', role: 'admin' };
  const canWrite = () => ME.role === 'admin' || ME.role === 'operator';
  async function loadMe() {
    try { ME = await api('/api/me'); } catch (e) { ME = { label: '', role: 'viewer' }; }
    document.body.dataset.role = ME.role;
    const b = $('#role-badge');
    b.textContent = (ROLE_LABEL[ME.role] || ME.role) + (ME.label ? ' · ' + ME.label : '');
    b.title = 'Oturum açan anahtar: ' + (ME.label || '-') + ' — rol: ' + (ROLE_LABEL[ME.role] || ME.role);
    b.hidden = false;
  }
  // İzleyici rolünde değiştirme düğmelerini devre dışı bırakır (asıl denetim sunucudadır: 403).
  const KEEP_SEL = '#login, .top-right, #findbar, .filters, #report-modal, .report-cards, .seg, .ctx-menu, .lightbox, .keep';
  const WRITE_TXT = /^(Sil|Ekle|Kaydet|Değiştir|Düzenle|Oluştur|Güncelle|Çalıştır|Şimdi|Kaydı başlat|Kaydı bitir|Bağlantı ekle|Probe ekle|Kural ekle|Anahtar oluştur|Senaryoyu kaydet|Giriş|Girişi|Bilgileri|Adımı|\+ Adım|Gruptaki|Kaydet ve|Yerleşimi sıfırla)/;
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
      $('#meter-label').textContent = `${lic.assets_used} / ${lic.asset_limit} asset`;
      const pct = lic.asset_limit ? Math.min(100, (lic.assets_used / lic.asset_limit) * 100) : 100;
      const fill = $('#meter-fill');
      fill.style.width = pct + '%';
      fill.className = pct >= 100 ? 'full' : pct >= 80 ? 'high' : '';
      return o;
    } catch (e) { return null; }
  }

  function buildNav() {
    const nav = $('#nav');
    nav.replaceChildren(...VIEWS.filter(([id]) => ME.role === 'admin' || !ADMIN_ONLY.includes(id)).map(([id, text]) => h('a', { href: '#' + id, 'data-v': id }, text)));
  }

  // ---- Görünümler ----
  let selectedAsset = null;

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
  function kpi(label, value, sub, extra, tone) {
    const kv = h('span', { class: 'kv' + (tone ? ' ' + tone : '') }); countUp(kv, value);
    return h('div', { class: 'panel kpi' }, h('span', { class: 'muted small' }, label),
      kv, h('span', { class: 'muted small' }, sub), extra || null);
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
      return h('div', { class: 'rk' }, h('span', { class: 'rk-name kl' }, it.kind ? kindIcon(it.kind, 30) : null, it.label),
        h('span', { class: 'track' }, fill), h('span', { class: 'rk-val mono' }, it.text || (fmt(it.value) + (unit || ''))));
    }));
  }

  // Kontrol edilen sayfaların küçültülmüş görüntüleri; çerçeve rengi durumu gösterir.
  function showShot(url, title) {
    const close = () => { ov.remove(); document.removeEventListener('keydown', onKey); };
    const onKey = (e) => { if (e.key === 'Escape') close(); };
    const ov = h('div', { class: 'lightbox', role: 'dialog', 'aria-label': title, onclick: close }, h('img', { src: url, alt: title }), h('p', null, title));
    document.addEventListener('keydown', onKey);
    document.body.append(ov);
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
    return h('div', { class: 'chg-box', onclick: (e) => e.stopPropagation() },
      h('div', { class: 'small' }, h('b', null, 'Neden değişti? '), ch ? h('span', { class: 'muted' }, ago(ch.at)) : null), body, btn);
  }

  function pageFrames(scenarios) {
    const list = scenarios.filter((x) => x.kind === 'browser');
    if (!list.length) return null;
    const state = (sc) => {
      if (sc.last_ok === false) return ['bad', 'Başarısız'];
      if (sc.last_changed_at && Date.now() - new Date(sc.last_changed_at) < 864e5) return ['chg', 'Sayfa değişti'];
      if (sc.last_ok === true) return ['ok', 'Sağlıklı'];
      return ['wait', 'Bekliyor'];
    };
    const order = { ok: 0, chg: 1, wait: 2, bad: 3 };
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
          h('span', { class: 'row small' }, pill(cls === 'bad' ? 'down' : cls === 'chg' ? 'warning' : cls === 'ok' ? 'up' : 'unknown', label),
            h('span', { class: 'mono muted' }, ago(sc.last_run))),
          sc.last_ok === false && sc.last_error ? h('span', { class: 'error small' }, sc.last_error.slice(0, 90)) : null,
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
    return h('section', null, h('h3', null, 'Kontrol edilen sayfalar'),
      h('div', { class: 'legend' }, h('span', { class: 'lg up' }, 'Sağlıklı'), h('span', { class: 'lg warn' }, 'Sayfa değişti (24 sa)'), h('span', { class: 'lg down' }, 'Başarısız'), h('span', { class: 'lg unknown' }, 'Bekliyor')),
      grid, restore,
      h('p', { class: 'muted small' }, 'Kartları sürükleyerek sıralayın; sağ tık veya × ile dashboard\'dan kaldırın (senaryo silinmez). Görüntü, tarayıcı senaryolarının son kontrolündeki sayfadır (yalnızca son 10 çalıştırma saklanır). Büyütmek için tıklayın.'));
  }

  async function viewGenel() {
    const [o, d, alerts, topo, tstats] = await Promise.all([refreshHeader(), api('/api/dashboard'), api('/api/alerts?state=open'), api('/api/topology').catch(() => null), api('/api/topology/stats').catch(() => ({}))]);
    const st = o.by_status;
    const total = Object.values(st).reduce((a, b) => a + b, 0);
    const av = d.availability_24h;
    const crit = (d.alerts_by_severity || {}).critical || 0;
    const scOk = d.scenarios.filter((x) => x.runs.length && x.runs[x.runs.length - 1].ok).length;
    const seg = (k) => { const e = h('span', { class: k }); e.style.width = (total ? ((st[k] || 0) / total) * 100 : 0) + '%'; return e; };

    const kpis = h('div', { class: 'kpis' },
      kpi('Erişilebilirlik (24 saat)', av == null ? '—' : av.toFixed(av >= 99.95 ? 1 : 2) + '%', av == null ? 'veri yok' : 'probe ve senaryo ortalaması', miniLine(d.availability), av != null && av < 99 ? 'bad' : ''),
      kpi('Açık alarm', String(o.open_alerts), crit ? `${crit} kritik` : 'kritik alarm yok', null, crit ? 'bad' : ''),
      kpi('Asset', `${st.up || 0} / ${total}`, `${st.down || 0} erişilemiyor, ${st.offline || 0} çevrimdışı`, null, (st.down || 0) ? 'bad' : ''),
      kpi('Ort. yanıt süresi (1 sa)', d.avg_latency_ms == null ? '—' : fmt(d.avg_latency_ms) + ' ms', 'probe gecikmesi'),
      kpi('Senaryolar', d.scenarios.length ? `${scOk} / ${d.scenarios.length}` : '—', d.scenarios.length ? 'son çalıştırması başarılı' : 'henüz senaryo yok', null, d.scenarios.length && scOk < d.scenarios.length ? 'bad' : ''));

    const availRows = d.availability.map((v, i) => h('tr', null, h('td', { class: 'mono' }, hourLabel(i)), h('td', { class: 'mono' }, v == null ? '—' : v.toFixed(2) + '%')));
    const alertRows = d.alerts_critical.map((c, i) => h('tr', null, h('td', { class: 'mono' }, hourLabel(i)), h('td', { class: 'mono' }, c), h('td', { class: 'mono' }, d.alerts_other[i])));

    const statusPanel = h('div', { class: 'panel stack' }, h('h3', null, 'Asset durumu'),
      total ? h('div', { class: 'bar', role: 'img', 'aria-label': 'Asset durum dağılımı' }, ['up', 'down', 'offline', 'unknown'].map(seg)) : h('p', { class: 'muted' }, 'Henüz asset yok.'),
      h('div', { class: 'legend' }, ['up', 'down', 'offline', 'unknown'].map((k) => h('span', { class: 'lg ' + k }, `${STATUS[k]}: ${st[k] || 0}`))),
      h('h3', null, 'Türe göre'),
      Object.keys(d.by_kind).length ? rankBars(Object.entries(Object.entries(d.by_kind).reduce((m, [k, n]) => { const kk = k === 'url' ? 'website' : k; m[kk] = (m[kk] || 0) + n; return m; }, {})).sort((a, b) => b[1] - a[1]).map(([k, n]) => ({ kind: k, label: KIND[k] || k, value: n, text: String(n) }))) : h('p', { class: 'muted' }, 'Henüz veri yok.'));

    const scen = d.scenarios.length ? h('div', { class: 'panel scn' }, d.scenarios.map((sc) => {
      const last = sc.runs[sc.runs.length - 1];
      const okMs = sc.runs.filter((r) => r.ok).map((r) => r.ms);
      const avg = okMs.length ? okMs.reduce((a, b) => a + b, 0) / okMs.length : null;
      return h('div', { class: 'scn-row' },
        h('div', { class: 'scn-name' }, h('b', null, sc.name), ' ', pill('info', sc.kind === 'browser' ? 'Tarayıcı' : 'HTTP'),
          sc.last_changed_at && Date.now() - new Date(sc.last_changed_at) < 864e5 ? [' ', pill('warning', 'sayfa değişti'), ' ', h('button', { type: 'button', class: 'small', onclick: async () => { await api(`/api/scenarios/${sc.id}/ack-changes`, { method: 'POST' }); render(); } }, 'Temizle')] : null,
          h('div', { class: 'muted small' }, sc.asset)),
        sc.runs.length ? strip(sc.runs) : h('span', { class: 'muted small' }, 'henüz çalışmadı'),
        h('div', { class: 'scn-stat' }, last ? (last.ok ? pill('up', 'Başarılı') : pill('down', 'Başarısız')) : pill('unknown', 'Bekliyor'),
          avg != null ? h('div', { class: 'mono small muted' }, `ort. ${fmt(avg)} ms`) : null));
    }), h('p', { class: 'muted small' }, 'Her çubuk bir çalıştırma: yükseklik süreyi, kırmızı tam çubuk başarısızlığı gösterir (son 30).')) : null;

    let mapSection = null;
    if (topo && topo.nodes.length) {
      const info = h('div', { class: 'map-info' });
      let sel = null;
      const map = drawTopology(topo, { stats: tstats, height: 460, webOnly: true, onSelect: (id) => { sel = sel === id ? null : id; map.setFocus(sel); paintInfo(); } });
      // Seçili uygulamayı "Kontrol edilen sayfalar" bölümüne ekler (tarayıcı senaryosu olanlar için).
      const pageButton = (n) => {
        if (n.kind === 'external') return null;
        const mine = d.scenarios.filter((x) => x.kind === 'browser' && x.asset === n.name);
        if (!mine.length) return h('button', { type: 'button', title: 'Bu uygulamanın tarayıcı senaryosu yok; Senaryolar > Tarayıcıdan kaydet ile oluşturun.', onclick: () => { location.hash = 'senaryolar'; } }, 'Kontrol edilen sayfalara ekle…');
        let hid = []; try { hid = JSON.parse(localStorage.getItem('apm_dash_hidden') || '[]'); } catch (e) { hid = []; }
        const off = mine.filter((x) => hid.includes(x.id));
        if (!off.length) return h('button', { type: 'button', disabled: true, title: 'Bu uygulama zaten sayfanın üstündeki "Kontrol edilen sayfalar" bölümünde.' }, 'Kontrol edilen sayfalarda ✓');
        return h('button', { type: 'button', class: 'primary', onclick: () => { try { localStorage.setItem('apm_dash_hidden', JSON.stringify(hid.filter((i) => !off.some((x) => x.id === i)))); } catch (e) { /* */ } render(); } }, 'Kontrol edilen sayfalara ekle');
      };
      const paintInfo = () => {
        if (sel == null) { info.replaceChildren(h('span', { class: 'muted small' }, 'Haritada yalnızca web uygulamaları görünür. Birine tıklayın: ona bağlı bileşenler açılır ve kutularda istatistikleri görünür. Kutuları sürükleyerek yerleştirebilirsiniz.')); return; }
        const n = topo.nodes.find((x) => x.id === sel), c = map.connected(sel);
        info.replaceChildren(h('span', { class: 'kl' }, kindIcon(n.kind, 34), h('b', null, n.name), pill(n.status, STATUS[n.status])),
          h('span', { class: 'muted small' }, `${c.dependents} bileşen buna bağımlı · bu ${c.dependencies} bileşene bağımlı`),
          h('span', { class: 'btns' }, h('button', { type: 'button', onclick: () => { sel = null; map.setFocus(null); paintInfo(); } }, 'Yalnızca web uygulamaları'),
            h('button', { type: 'button', onclick: () => { selectedNode = sel; location.hash = 'topoloji'; } }, 'Topolojide göster'),
            pageButton(n)));
      };
      paintInfo();
      mapSection = h('section', null, h('h3', null, 'Uygulama haritası'), h('p', { class: 'muted small' }, 'Web uygulamalarına tıklayınca bağlı bileşenleri açılır. Kutuları fareyle tutup sürükleyebilirsiniz; yerleşim tarayıcıda saklanır.'), h('div', { class: 'topo-wrap map' }, map), info);
    }
    return h('div', null,
      pageFrames(d.scenarios),
      h('section', null, h('h2', null, 'Genel bakış'), kpis), mapSection,
      h('div', { class: 'dash2' },
        h('section', null, h('h3', null, 'Erişilebilirlik · son 24 saat'), h('div', { class: 'panel' }, availChart(d.availability),
          h('details', { class: 'adv' }, h('summary', null, 'Tablo olarak göster'), table(['Saat', 'Erişilebilirlik'], availRows, '')))),
        h('section', null, statusPanel)),
      h('div', { class: 'dash2 even' },
        h('section', null, h('h3', null, 'Alarmlar · son 24 saat'), h('div', { class: 'panel' },
          h('div', { class: 'legend' }, h('span', { class: 'lg crit' }, 'Kritik'), h('span', { class: 'lg warn' }, 'Uyarı / bilgi')),
          alertsChart(d.alerts_critical, d.alerts_other),
          h('details', { class: 'adv' }, h('summary', null, 'Tablo olarak göster'), table(['Saat', 'Kritik', 'Diğer'], alertRows, '')))),
        h('section', null, h('h3', null, 'En yavaş hedefler · son 1 saat'), h('div', { class: 'panel' },
          d.slowest.length ? rankBars(d.slowest.map((x) => ({ kind: x.kind, label: x.asset, value: x.avg_ms, text: `${fmt(x.avg_ms)} ms (en yüksek ${fmt(x.max_ms)})` })), ' ms') : h('p', { class: 'muted' }, 'Henüz probe gecikmesi verisi yok.')))),
      scen ? h('section', null, h('h3', null, 'Senaryo sağlığı'), scen) : null,
      h('section', null, h('h3', null, 'Açık alarmlar'), alertTable(alerts, false)));
  }

  function alertTable(list, withResolved) {
    const head = ['Önem', 'Kural', 'Asset', 'Değer', 'Açılış'].concat(withResolved ? ['Kapanış'] : []);
    return table(head, list.map((a) => h('tr', null,
      h('td', null, pill(a.severity, SEV[a.severity])),
      h('td', null, a.rule),
      h('td', null, a.asset),
      h('td', { class: 'mono' }, `${fmt(a.value)} (${a.comparator} ${a.threshold})`),
      h('td', { class: 'mono small' }, ago(a.opened_at)),
      withResolved ? h('td', { class: 'mono small' }, a.resolved_at ? ago(a.resolved_at) : '—') : null)),
      'Açık alarm yok.');
  }

  async function viewAssetler() {
    await refreshHeader();
    const assets = await api('/api/assets');
    const rows = assets.map((a) => h('tr', { class: 'click' + (a.id === selectedAsset ? ' sel' : ''), tabindex: '0', 'data-id': a.id,
      'data-search': [a.name, KIND[a.kind], a.kind, STATUS[a.status], LANG[a.language]].join(' ').toLowerCase(), 'data-status': a.status, 'data-kind': a.kind === 'url' ? 'website' : a.kind, 'data-coll': a.collection },
      h('td', null, h('span', { class: 'kl' }, kindIcon(a.kind, 30), h('b', null, a.name))),
      h('td', null, KIND[a.kind] || a.kind),
      h('td', null, LANG[a.language]),
      h('td', null, a.collection === 'agent' ? 'Agent' : 'Agent\'sız'),
      h('td', null, pill(a.status, STATUS[a.status])),
      h('td', { class: 'mono' }, a.latest['host.cpu_pct'] != null ? fmt(a.latest['host.cpu_pct']) + '%' : '—'),
      h('td', { class: 'mono' }, a.latest['host.mem_pct'] != null ? fmt(a.latest['host.mem_pct']) + '%' : '—'),
      h('td', { class: 'mono' }, a.latest['probe.latency_ms'] != null ? fmt(a.latest['probe.latency_ms']) + ' ms' : '—'),
      h('td', { class: 'mono small' }, ago(a.last_seen))));
    const t = table(['Ad', 'Tür', 'Dil', 'Toplama', 'Durum', 'CPU', 'Bellek', 'Gecikme', 'Son veri'], rows,
      'Henüz asset yok. Probe\'lar sayfasından izleme ekleyin veya bir agent bağlayın.');
    const detail = h('section', { id: 'detail' });
    const open = (id) => { selectedAsset = id; render(); };
    t.addEventListener('click', (e) => { const tr = e.target.closest('tr[data-id]'); if (tr) open(Number(tr.dataset.id)); });
    t.addEventListener('keydown', (e) => { if (e.key === 'Enter') { const tr = e.target.closest('tr[data-id]'); if (tr) open(Number(tr.dataset.id)); } });
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
      { key: 'coll', label: 'Toplama', type: 'select', options: [['agent', 'Agent'], ['agentless', 'Agent\'sız']] }], t, assets.length);
    const wrap = h('div', { style: null }, h('section', null, h('h2', null, 'Assetler'), sum, fb, t), detail);
    const sel = assets.find((a) => a.id === selectedAsset);
    if (sel) await fillDetail(detail, sel);
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

  async function fillDetail(root, asset) {
    const data = await api(`/api/assets/${asset.id}/metrics?minutes=60`);
    const kindSel = select('a-kind', KIND_OPTS, asset.kind === 'url' ? 'website' : asset.kind);
    kindSel.addEventListener('change', async () => {
      try { await api('/api/assets/' + asset.id, { method: 'PATCH', body: { kind: kindSel.value } }); render(); } catch (e) { alert(e.message); }
    });
    const names = Object.keys(data).sort();
    root.replaceChildren(
      h('div', { class: 'row' }, h('h3', null, `${asset.name}: son 60 dakika`),
        h('span', { class: 'row' }, h('label', { class: 'check' }, 'Tür (topolojideki şekil)', kindSel),
        h('button', { type: 'button', class: 'danger', onclick: async () => {
          if (!window.confirm(`"${asset.name}" silinsin mi? Tüm metrikleri de silinir.`)) return;
          try { await api('/api/assets/' + asset.id, { method: 'DELETE' }); selectedAsset = null; render(); } catch (e) { alert(e.message); }
        } }, 'Asset\'i sil'))),
      names.length
        ? h('div', { class: 'panel sparks' }, names.map((n) => {
            const pts = data[n];
            return h('div', { class: 'spark' }, h('span', { class: 'mono small muted' }, n), spark(pts),
              h('b', { class: 'mono' }, fmt(pts[pts.length - 1][1])));
          }))
        : h('p', { class: 'muted' }, 'Son 60 dakikada metrik yok.'));
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

  const PROBE_TYPES = [['http', 'HTTP / HTTPS'], ['tcp', 'TCP bağlantı'], ['icmp', 'ICMP (ping)'], ['snmp', 'SNMP (v1/v2c)'], ['dns', 'DNS sorgusu'],
    ['tls', 'TLS sertifikası (süre)'], ['udp', 'UDP'], ['ntp', 'NTP (saat sapması)'], ['postgres', 'PostgreSQL']];
  const PROBE_HINTS = { http: 'https://ornek.com/health', tcp: 'sunucu.ornek.com:5432', icmp: '10.0.0.1 veya sunucu.ornek.com', snmp: '10.0.0.1 veya 10.0.0.1:161',
    dns: 'ornek.com', tls: 'ornek.com veya ornek.com:8443', udp: 'sunucu.ornek.com:514', ntp: 'pool.ntp.org',
    postgres: 'postgres://kullanici:parola@host:5432/veritabani?sslmode=disable' };
  const PROBE_HELP = { icmp: 'Paket kaybı % ve ortalama RTT ölçülür. Konteynerde ayrıcalıksız ICMP için sysctl gerekir (docker-compose.yml içinde ayarlı).',
    snmp: 'Topluluk adı gizli saklanır ve listede gösterilmez. SNMPv3 desteklenmez. Sayısal değer "probe.snmp_value" metriği olur.',
    dns: 'Çözümleme süresi ölçülür; "Beklenen" doluysa yanıtta aranır. Özel DNS sunucusu verebilirsiniz.',
    tls: 'Sertifikanın kalan gün sayısı "probe.cert_days_left" metriği olur (alarm kuralı: Sertifika süresi doluyor).',
    udp: 'UDP bağlantısızdır: yanıt gelirse başarılı sayılır. Yanıt vermeyen servisler için uygun değildir.',
    ntp: 'Sunucu saatinden sapma (ms) "probe.ntp_offset_ms" metriği olur.',
    tcp: 'İsteğe bağlı: bağlanınca metin gönderip yanıtta metin arayabilirsiniz (ör. SSH- banner\'ı, Redis PING).' };
  const SNMP_OIDS = [['1.3.6.1.2.1.1.3.0', 'sysUpTime'], ['1.3.6.1.2.1.1.1.0', 'sysDescr'], ['1.3.6.1.2.1.1.5.0', 'sysName'], ['1.3.6.1.2.1.2.1.0', 'ifNumber (arayüz sayısı)'], ['1.3.6.1.2.1.25.3.3.1.2.1', 'hrProcessorLoad (CPU %)']];

  function optSummary(p) {
    const o = p.options || {}, r = [];
    if (o.count) r.push(o.count + ' paket');
    if (p.type === 'snmp') r.push('OID ' + (o.oid || '1.3.6.1.2.1.1.3.0'), 'v' + (o.snmp_version || '2c'), p.has_community ? 'topluluk gizli' : 'topluluk: public');
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
    const msg = h('p', { class: 'msg', role: 'status' });
    const f = h('form', { class: 'inline', id: 'probe-form' },
      field('Asset adı', h('input', { id: 'p-asset', required: true, maxlength: '200', placeholder: 'ör. odeme-servisi' })),
      field('Dil (isteğe bağlı)', select('p-lang', Object.entries(LANG).map(([k, v]) => [k, k === 'none' ? 'Belirtilmedi' : v]))),
      field('Probe türü', select('p-type', PROBE_TYPES.map(([k, l]) => [k, l]))),
      field('Asset türü (topoloji şekli)', select('p-kind', KIND_AUTO)),
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
    // Türe özgü seçenekler: her alan hangi türlerde görüneceğini 'types' ile belirtir.
    const optFields = [
      ['icmp', field('Paket sayısı', h('input', { id: 'o-count', type: 'number', min: '1', max: '10', value: '3' }))],
      ['snmp', field('Topluluk adı', h('input', { id: 'o-comm', type: 'password', autocomplete: 'off', placeholder: 'public' }))],
      ['snmp', field('OID', h('input', { id: 'o-oid', list: 'o-oids', placeholder: '1.3.6.1.2.1.1.3.0' })), h('datalist', { id: 'o-oids' }, SNMP_OIDS.map(([v, l]) => h('option', { value: v }, l)))],
      ['snmp', field('Sürüm', select('o-ver', [['2c', 'v2c'], ['1', 'v1']]))],
      ['dns', field('Kayıt türü', select('o-rec', ['A', 'AAAA', 'MX', 'TXT', 'CNAME', 'NS'].map((x) => [x, x])))],
      ['dns', field('DNS sunucusu (isteğe bağlı)', h('input', { id: 'o-srv', placeholder: '8.8.8.8 veya 10.0.0.53:53' }))],
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
      optBox.hidden = !optItems.some((it) => it.types.includes(t));
      tlsBox.hidden = t !== 'tls';
      help.textContent = PROBE_HELP[t] || ''; help.hidden = !PROBE_HELP[t];
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
        if (t === 'snmp') Object.assign(o, { community: $('#o-comm', f).value, oid: val('#o-oid'), snmp_version: val('#o-ver') });
        if (t === 'dns') Object.assign(o, { record: val('#o-rec'), server: val('#o-srv') });
        if (t === 'tcp' || t === 'udp') o.send = $('#o-send', f).value;
        if (['tcp', 'udp', 'snmp', 'dns'].includes(t)) o.expect = $('#o-expect', f).value;
        if (t === 'tls') body.verify_tls = $('#o-tlsverify', f).checked;
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
        const cm = h('input', { type: 'password', autocomplete: 'new-password', placeholder: p.has_community ? 'değiştirmek için yazın (boş = aynı)' : 'public' }), oid = inp(o.oid, { list: 'pe-oids' }), ver = select(null, [['2c', 'v2c'], ['1', 'v1']], o.snmp_version || '2c'), ex = inp(o.expect);
        extra.push(h('datalist', { id: 'pe-oids' }, SNMP_OIDS.map(([v, t]) => h('option', { value: v }, t))), h('div', { class: 'inline' }, field('Topluluk adı', cm), field('OID', oid), field('Sürüm', ver), field('Yanıtta aranacak metin', ex)));
        collect = () => ({ options: { community: cm.value, oid: oid.value.trim(), snmp_version: ver.value, expect: ex.value } });
      } else if (p.type === 'dns') {
        const rec = select(null, ['A', 'AAAA', 'MX', 'TXT', 'CNAME', 'NS'].map((x) => [x, x]), o.record || 'A'), sv = inp(o.server, { placeholder: 'isteğe bağlı' }), ex = inp(o.expect);
        extra.push(h('div', { class: 'inline' }, field('Kayıt türü', rec), field('DNS sunucusu', sv), field('Yanıtta aranacak metin', ex)));
        collect = () => ({ options: { record: rec.value, server: sv.value.trim(), expect: ex.value } });
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
      h('td', null, h('span', { class: 'row' }, h('button', { type: 'button', onclick: () => editProbe(p) }, 'Değiştir'), h('button', { type: 'button', class: 'danger', onclick: async () => { try { await api('/api/probes/' + p.id, { method: 'DELETE' }); render(); } catch (e) { alert(e.message); } } }, 'Sil')))));
    const types = {}; probes.forEach((p) => { types[p.type] = (types[p.type] || 0) + 1; });
    const pSum = h('div', { class: 'kpis' },
      kpi('Probe sayısı', String(probes.length), 'tanımlı', null, ''),
      ...Object.entries(types).sort((a, b) => b[1] - a[1]).slice(0, 4).map(([k, n]) => kpi(k.toUpperCase(), String(n), 'probe', null, '')));
    return h('div', { style: null },
      h('section', null, h('h2', null, 'Agent\'sız izleme (probe)'), probes.length ? pSum : null,
        h('p', { class: 'muted' }, 'Sunucunuza hiçbir şey kurmadan HTTP, TCP, ICMP (ping), SNMP, DNS, TLS sertifikası, UDP, NTP ve PostgreSQL erişilebilirliğini ve gecikmesini izler. Yeni bir asset adı yazarsanız lisanstan bir asset düşer.'),
        h('p', { class: 'muted' }, 'APP_MON Docker içinde çalışıyorsa localhost konteynerin kendisidir. Windows veya Mac\'teki bir uygulamayı izlemek için localhost yerine host.docker.internal yazın (ör. host.docker.internal:43130).'),
        h('div', { class: 'panel' }, f, msg)),
      h('section', null, (() => {
        const t = table(['Asset', 'Tür', 'Hedef', 'Aralık', 'Son çalışma', 'Son sonuç', ''], rows, 'Henüz probe tanımlanmadı.');
        const fb = filterBar(FILTERS.probelar, [{ key: 'q', label: 'Ara (asset, hedef, hata…)', type: 'text' },
          { key: 'type', label: 'Tür', type: 'select', options: [...new Set(probes.map((p) => p.type))].map((x) => [x, x.toUpperCase()]) },
          { key: 'res', label: 'Sonuç', type: 'select', options: [['ok', 'Başarılı'], ['fail', 'Hatalı'], ['wait', 'Bekliyor']] }], t, probes.length);
        return h('div', null, fb, t);
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

  function recorderPanel(assets) {
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
        h('div', { class: 'stack' }, h('h3', null, 'Kaydedilen adımlar'), stepsList, hint, h('div', { class: 'row' }, finish, cancel))), msg, out);
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
      const f = h('form', { class: 'stack' },
        h('div', { class: 'inline' },
          field('Asset adı', h('input', { id: 'rv-asset', required: true, maxlength: '200', list: 'rv-assets', placeholder: 'ör. mobil-uygulama' })),
          field('Senaryo adı', h('input', { id: 'rv-name', required: true, maxlength: '200', placeholder: 'ör. Giriş ve sipariş' })),
          field('Asset türü', select('rv-kind', KIND_OPTS, 'website')),
          field('Aralık (sn, en az 60)', h('input', { id: 'rv-interval', type: 'number', min: '60', value: '300' })),
          field('Adım zaman aşımı (ms)', h('input', { id: 'rv-timeout', type: 'number', min: '500', max: '60000', value: '10000' }))),
        h('datalist', { id: 'rv-assets' }, assets.map((a) => h('option', { value: a.name }))),
        d.secret_names.length ? h('p', { class: 'muted small' }, `${d.secret_names.length} gizli alan (parola) kaydedildi; değerleri sunucuda ${d.secret_names.join(', ')} değişkenlerinde saklanır (veritabanında düz metin) ve burada gösterilmez.`) : null,
        authPanel,
        rowsBox,
        h('div', { class: 'row' },
          h('span', { class: 'muted small' }, 'Her çalıştırma temiz bir tarayıcı profilinde başlar; giriş adımları da kaydedilmiş olmalıdır.'),
          h('span', { class: 'row' }, h('button', { type: 'button', id: 'rv-test' }, 'Dene (kaydetmeden)'), h('button', { type: 'submit', class: 'primary' }, 'Senaryoyu kaydet'))));
      const collect = () => {
        const authOn = $('#au-on', f).checked, mode = f.querySelector('input[name=au-mode]:checked').value;
        const drop = authOn && mode === 'form' && $('#au-drop', f) && $('#au-drop', f).checked;
        const list = [...rowsBox.querySelectorAll('.step')].filter((row) => !(drop && suggested.has(row._st))).map((row) => ({
          ...row._st, name: $('.r-name', row).value.trim() || row._st.name,
          expect_text: $('.r-text', row).value || undefined, max_ms: Number($('.r-max', row).value) > 0 ? Number($('.r-max', row).value) : undefined,
          login: authOn && mode === 'steps' && $('.r-login', row).checked ? true : undefined }));
        const body = { kind: 'browser', draft_id: d.draft_id, browser_steps: list, asset_name: $('#rv-asset', f).value, name: $('#rv-name', f).value,
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

  async function viewSenaryolar() {
    await refreshHeader();
    const [list, assets] = await Promise.all([api('/api/scenarios'), api('/api/assets')]);
    const msg = h('p', { class: 'msg', role: 'status' });
    const out = h('div');
    const stepsBox = h('div', { class: 'steps' });
    const addStep = () => {
      const c = stepCard(stepsBox.children.length + 1, () => { c.remove(); [...stepsBox.children].forEach((x, i) => { $('legend', x).textContent = 'Adım ' + (i + 1); }); });
      stepsBox.append(c);
    };
    addStep();
    const f = h('form', { id: 'sc-form', class: 'stack' },
      h('div', { class: 'inline' },
        field('Asset adı', h('input', { id: 'sc-asset', required: true, maxlength: '200', list: 'sc-assets', placeholder: 'ör. mobil-uygulama' })),
        field('Senaryo adı', h('input', { id: 'sc-name', required: true, maxlength: '200', placeholder: 'ör. Giriş ve liste' })),
        field('Asset türü', select('sc-kind', KIND_AUTO)),
        field('Aralık (sn, en az 30)', h('input', { id: 'sc-interval', type: 'number', min: '30', value: '300' })),
        field('Toplam zaman aşımı (ms)', h('input', { id: 'sc-timeout', type: 'number', min: '500', max: '60000', value: '10000' })),
        h('label', { class: 'check' }, h('input', { id: 'sc-tls', type: 'checkbox', checked: true }), 'TLS sertifikasını doğrula')),
      h('datalist', { id: 'sc-assets' }, assets.map((a) => h('option', { value: a.name }))),
      field('Değişkenler (satır başına "ad=değer"; adımlarda {{ad}} olarak kullanılır)', area('sc-vars', 3, 'kullanici=test\nparola=...')),
      stepsBox,
      h('div', { class: 'row' },
        h('button', { type: 'button', onclick: addStep }, '+ Adım ekle'),
        h('span', { class: 'row' },
          h('button', { type: 'button', id: 'sc-test' }, 'Dene (kaydetmeden)'),
          h('button', { type: 'submit', class: 'primary' }, 'Senaryoyu kaydet'))));
    const collect = () => ({
      asset_name: $('#sc-asset', f).value, name: $('#sc-name', f).value, interval_sec: Number($('#sc-interval', f).value),
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
          h('td', { class: 'small' }, r.error || (r.steps || []).map((x) => x.name + ' ' + fmt(x.ms) + ' ms').join(' → ') + ((r.steps || []).some((x) => (x.added || []).length + (x.removed || []).length) ? ' · sayfa değişti' : '')))), 'Henüz çalıştırma yok.'),
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
      const form = h('form', { class: 'stack' },
        h('div', { class: 'inline' }, field('Senaryo adı', nm), field('Aralık (sn)', iv), field('Zaman aşımı (ms)', to)),
        h('div', { class: 'row' }, h('label', { class: 'check' }, en, 'Etkin'), h('label', { class: 'check' }, tls, 'TLS sertifikasını doğrula')),
        authBox, h('h4', null, 'Adımlar'),
        h('p', { class: 'muted small' }, 'Başlık, gövde ve değişkenler gibi gizli alanlar korunur; burada yalnızca görünen alanlar düzenlenir. Adımları ↑/↓ ile sıralayabilir veya silebilirsiniz. Adımlar değişirse sayfa değişikliği taban çizgisi yeniden başlar.'),
        stepsBox, h('div', { class: 'row' }, save, h('button', { type: 'button', onclick: () => editBox.replaceChildren() }, 'Vazgeç')), emsg);
      form.addEventListener('submit', async (e) => {
        e.preventDefault(); save.disabled = true; emsg.className = 'msg'; emsg.textContent = 'Kaydediliyor…';
        const body = { name: nm.value, interval_sec: Number(iv.value), timeout_ms: Number(to.value), verify_tls: tls.checked, enabled: en.checked,
          steps: items.map((it) => ({ from: it.from, name: it.name || '', method: it.method || '', url: it.url || '', expect_status: it.expect_status || '', expect_text: it.expect_text || '', max_ms: Number(it.max_ms || 0), selector: it.selector || '', value: it.value || '', key: it.key || '', login: !!it.login })) };
        const a = getAuth(); if (a) body.auth = a;
        try { await api('/api/scenarios/' + sc.id, { method: 'PUT', body }); render(); } catch (x) { emsg.className = 'msg error'; emsg.textContent = x.message; save.disabled = false; }
      });
      editBox.replaceChildren(h('section', null, h('h3', null, 'Senaryoyu düzenle: ' + sc.name), h('div', { class: 'panel' }, form)));
      editBox.scrollIntoView({ block: 'start', behavior: 'smooth' });
    };
    const rows = list.map((sc) => h('tr', { class: 'click', onclick: () => showRuns(sc).catch((x) => { msg.className = 'msg error'; msg.textContent = x.message; }) },
      h('td', null, h('b', null, sc.name), ' ', pill('info', sc.kind === 'browser' ? 'Tarayıcı' : 'HTTP'), sc.last_changed_at && Date.now() - new Date(sc.last_changed_at) < 864e5 ? [' ', pill('warning', 'Sayfa değişti ' + ago(sc.last_changed_at))] : null,
        sc.auth && sc.auth.blocked ? [' ', pill('down', 'Giriş durduruldu')] : sc.auth && sc.auth.login_fails ? [' ', pill('warning', `Giriş hatası ${sc.auth.login_fails}/3`)] : null, h('div', { class: 'muted small' }, sc.asset)),
      h('td', { class: 'small' }, sc.steps.map((st, i) => h('div', null, (i + 1) + '. ' + st.method + ' ' + st.name))),
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
      h('section', null, h('h2', null, 'Senaryolar (bot)'),
        h('p', { class: 'muted' }, 'İzlediğiniz uygulamada yapılan işlemleri (giriş, liste, ayrıntı…) sıralı HTTP adımları olarak tanımlayın; APP_MON bunları belirlenen aralıkla çalıştırır, her adımın süresini ve doğruluğunu ölçer, hata olursa alarm üretir. Çerezler adımlar arasında taşınır; bir adımın yanıtından alınan değer sonraki adımlarda {{ad}} ile kullanılır. Docker içinden ana makinedeki uygulamaya host.docker.internal ile erişin.'),
        h('p', { class: 'muted' }, 'Not: Başlıklar, gövde ve değişkenler (parola/token gibi) veritabanında düz metin saklanır ve arayüzde bir daha gösterilmez; mümkünse bu iş için ayrılmış, yetkisi sınırlı bir test kullanıcısı kullanın.'),
        h('section', null, h('h3', null, 'Tarayıcıdan kaydet'),
          h('p', { class: 'muted' }, 'Gerçek bir tarayıcıda (sunucuda çalışan, panelin içinde görünen) adımları siz yaparsınız; APP_MON kaydeder ve istediğiniz aralıkla yeniden oynatır. Her adımın süresini, sayfa yapısındaki değişiklikleri ve sayfanın eriştiği dış alan adlarını (topolojide görünür) ölçer.'),
          h('div', { class: 'panel' }, recorderPanel(assets))),
        h('details', { class: 'sect-details' }, h('summary', null, 'HTTP adımlarıyla elle tanımla'), h('div', { class: 'panel' }, f, msg), out),
        editBox),
      h('section', null, table(['Senaryo', 'Adımlar', 'Aralık', 'Son çalışma', 'Son sonuç', ''], rows, 'Henüz senaryo tanımlanmadı.')),
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
    { group: 'Erişilebilirlik ve hız (probe)', items: [
      { name: 'Servis erişilemiyor', metric: 'probe.up', comparator: '<', threshold: 1, window_sec: 30, severity: 'critical', desc: 'Probe başarısız' },
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
  const ruleBody = (r) => ({ name: r.name, metric: r.metric, comparator: r.comparator, threshold: r.threshold, window_sec: r.window_sec, severity: r.severity });

  async function viewAlarmlar() {
    await refreshHeader();
    const [rules, open, resolved] = await Promise.all([api('/api/alert-rules'), api('/api/alerts?state=open'), api('/api/alerts?state=resolved')]);
    const msg = h('p', { class: 'msg', role: 'status' });
    let editing = null;
    const f = h('form', { class: 'inline' },
      field('Kural adı', h('input', { id: 'r-name', required: true, maxlength: '200' })),
      field('Metrik', h('input', { id: 'r-metric', required: true, placeholder: 'host.cpu_pct', maxlength: '128', list: 'r-metrics' })),
      h('datalist', { id: 'r-metrics' }, ['host.cpu_pct', 'host.mem_pct', 'host.disk_pct', 'probe.up', 'probe.latency_ms', 'probe.packet_loss_pct', 'probe.cert_days_left', 'probe.ntp_offset_ms', 'probe.snmp_value', 'probe.dns_answers', 'scenario.up', 'scenario.duration_ms', 'scenario.changes'].map((m) => h('option', { value: m }))),
      field('Koşul', select('r-cmp', [['>', 'büyükse (>)'], ['<', 'küçükse (<)']])),
      field('Eşik', h('input', { id: 'r-thr', type: 'number', step: 'any', required: true })),
      field('Pencere (sn)', h('input', { id: 'r-win', type: 'number', min: '10', value: '60' })),
      field('Önem', select('r-sev', [['warning', 'Uyarı'], ['critical', 'Kritik'], ['info', 'Bilgi']])),
      h('button', { type: 'submit', class: 'primary', id: 'r-save' }, 'Kural ekle'),
      h('button', { type: 'button', id: 'r-cancel', hidden: true }, 'Vazgeç'));
    const formTitle = h('h3', null, 'Yeni kural');
    const fill = (r) => {
      $('#r-name', f).value = r ? r.name : ''; $('#r-metric', f).value = r ? r.metric : '';
      $('#r-cmp', f).value = r ? r.comparator : '>'; $('#r-thr', f).value = r ? String(r.threshold) : '';
      $('#r-win', f).value = r ? String(r.window_sec) : '60'; $('#r-sev', f).value = r ? r.severity : 'warning';
      editing = r; formTitle.textContent = r ? `Kuralı düzenle: ${r.name}` : 'Yeni kural';
      $('#r-save', f).textContent = r ? 'Kaydet' : 'Kural ekle'; $('#r-cancel', f).hidden = !r;
      if (r) f.scrollIntoView({ block: 'center', behavior: 'smooth' });
    };
    $('#r-cancel', f).addEventListener('click', () => fill(null));
    f.addEventListener('submit', async (e) => {
      e.preventDefault();
      msg.className = 'msg'; msg.textContent = '';
      const body = { name: $('#r-name', f).value, metric: $('#r-metric', f).value, comparator: $('#r-cmp', f).value,
        threshold: Number($('#r-thr', f).value), window_sec: Number($('#r-win', f).value), severity: $('#r-sev', f).value };
      try {
        if (editing) await api('/api/alert-rules/' + editing.id, { method: 'PUT', body: { ...body, enabled: editing.enabled } });
        else await api('/api/alert-rules', { method: 'POST', body });
        render();
      } catch (err) { msg.className = 'msg error'; msg.textContent = err.message; }
    });
    const toggle = async (r) => { try { await api('/api/alert-rules/' + r.id, { method: 'PUT', body: { ...ruleBody(r), enabled: !r.enabled } }); render(); } catch (e) { alert(e.message); } };
    const rows = rules.map((r) => h('tr', { class: r.enabled ? '' : 'off' },
      h('td', null, h('b', null, r.name), r.enabled ? null : [' ', pill('unknown', 'kapalı')]), h('td', { class: 'mono small' }, `${r.metric} ${r.comparator} ${r.threshold}`),
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
    return h('div', null,
      h('section', null, h('h2', null, 'Açık alarmlar'), aSum, alertTable(open, false)),
      h('section', null, h('h3', null, 'Alarm kuralları'),
        h('p', { class: 'muted' }, 'Kural, pencere içindeki ortalama değeri eşikle karşılaştırır. Kuralı düzenleyince açık alarmları kapanır; koşul sürüyorsa yeniden açılır.'),
        table(['Ad', 'Koşul', 'Pencere', 'Önem', ''], rows, 'Kural yok.'),
        h('div', { class: 'panel stack' }, formTitle, f, msg)),
      h('section', null, h('h3', null, 'Hazır şablonlar'), h('p', { class: 'muted' }, 'Tek tıkla ekleyin ya da "Düzenleyip ekle" ile eşiği kendinize göre değiştirin.'), gallery),
      h('section', null, h('h3', null, 'Kapanan alarmlar'), alertTable(resolved, true)));
  }

  async function viewLisans() {
    const o = await refreshHeader();
    const lic = o.license;
    const keys = await api('/api/keys');
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
        h('div', { class: 'panel' }, lf, msg)),
      h('section', null, h('h2', null, 'API anahtarları'), keyOut,
        table(['Etiket', 'Rol', 'Oluşturulma', ''], krows, 'Anahtar yok.'),
        h('div', { class: 'panel' }, kf)),
      h('section', null, h('h2', null, 'Veri kaynağı bağlama'),
        h('div', { class: 'grid' },
          h('div', { class: 'panel' }, h('h3', null, '1. Agent\'sız (tercih edilen)'),
            h('p', { class: 'muted' }, '.NET, Java, Python ve Go uygulamaları OpenTelemetry SDK ile metriği doğrudan sunucuya yollar. Sunucuya kurulum gerekmez.'),
            h('pre', null, `OTEL_EXPORTER_OTLP_ENDPOINT=${origin}\nOTEL_EXPORTER_OTLP_PROTOCOL=http/json\nOTEL_EXPORTER_OTLP_HEADERS=X-API-Key=<agent anahtarı>\nOTEL_SERVICE_NAME=odeme-servisi`),
            h('p', { class: 'muted small' }, 'Bu sürüm yalnızca OTLP/HTTP JSON kabul eder. Protobuf gönderen SDK\'lar için OpenTelemetry Collector (otlphttp, encoding: json) kullanın.')),
          h('div', { class: 'panel' }, h('h3', null, '2. Hafif agent (isteğe bağlı)'),
            h('p', { class: 'muted' }, 'CPU, bellek ve disk gibi ana makine metrikleri için tek dosyalık Go agent\'ı (Linux).'),
            h('pre', null, `APM_SERVER=${origin} \\\nAPM_KEY=<agent anahtarı> \\\n./appmon-agent`)))));
  }


  // ---- Ayarlar ve tema ----
  const PALETTES = [['default', 'Varsayılan', '#0b7a75', '#f4f7f8'], ['ocean', 'Okyanus', '#1f66c9', '#eef4fb'], ['forest', 'Orman', '#2e7d4f', '#f1f6f0'], ['sunset', 'Gün batımı', '#c2570c', '#faf4ef'], ['rose', 'Gül', '#b8325c', '#faf1f4'], ['graphite', 'Grafit', '#3f4654', '#f3f3f4']];
  const ACCENTS = [['auto', 'Palete göre', ''], ['teal', 'Turkuaz', '#0b7a75'], ['blue', 'Mavi', '#2a6fd6'], ['violet', 'Mor', '#6250d6'], ['slate', 'Gri', '#475569']];
  function getPref(k, def) { try { return localStorage.getItem(k) || def; } catch (e) { return def; } }
  function setPref(k, v) { try { localStorage.setItem(k, v); } catch (e) { /* depolama kapalı: yalnızca bu oturumda geçerli */ } }
  function applyTheme(t) {
    if (t === 'light' || t === 'dark') document.documentElement.setAttribute('data-theme', t); else document.documentElement.removeAttribute('data-theme');
  }
  function applyAccent(a) { if (a && a !== 'auto' && a !== 'teal') document.documentElement.setAttribute('data-accent', a); else document.documentElement.removeAttribute('data-accent'); }
  function applyPalette(p) { if (p && p !== 'default') document.documentElement.setAttribute('data-palette', p); else document.documentElement.removeAttribute('data-palette'); }

  async function viewAyarlar() {
    await refreshHeader();
    const info = await api('/api/settings');
    let theme = getPref('apm_theme', 'system'), accent = getPref('apm_accent', 'auto'), palette = getPref('apm_palette', 'default');
    const themeBox = h('div', { class: 'seg', role: 'group', 'aria-label': 'Tema' });
    const palBox = h('div', { class: 'seg', role: 'group', 'aria-label': 'Renk paleti' });
    const accentBox = h('div', { class: 'seg', role: 'group', 'aria-label': 'Vurgu rengi' });
    const paint = () => {
      themeBox.replaceChildren(...[['system', 'Sistem'], ['light', 'Açık'], ['dark', 'Koyu']].map(([v, t]) =>
        h('button', { type: 'button', 'aria-pressed': String(theme === v), onclick: () => { theme = v; setPref('apm_theme', v); applyTheme(v); paint(); } }, t)));
      palBox.replaceChildren(...PALETTES.map(([v, t, c, bg]) => {
        const sw = h('span', { class: 'swatch pal' }); sw.style.background = c; sw.style.boxShadow = '0 0 0 4px ' + bg + ' inset';
        return h('button', { type: 'button', 'aria-pressed': String(palette === v), onclick: () => { palette = v; setPref('apm_palette', v); applyPalette(v); paint(); } }, sw, t);
      }));
      accentBox.replaceChildren(...ACCENTS.map(([v, t, c]) => {
        const sw = h('span', { class: 'swatch' }); if (c) sw.style.background = c; else sw.hidden = true;
        return h('button', { type: 'button', 'aria-pressed': String(accent === v), onclick: () => { accent = v; setPref('apm_accent', v); applyAccent(v); paint(); } }, sw, t);
      }));
    };
    paint();
    const fact = (k, v) => h('div', { class: 'fact' }, h('span', null, k), h('b', null, v));
    return h('div', null,
      h('section', null, h('h2', null, 'Ayarlar'),
        h('div', { class: 'panel stack' }, h('h3', null, 'Görünüm'),
          h('div', { class: 'stack' }, h('span', { class: 'muted small' }, 'Tema'), themeBox),
          h('div', { class: 'stack' }, h('span', { class: 'muted small' }, 'Renk paleti'), palBox),
          h('div', { class: 'stack' }, h('span', { class: 'muted small' }, 'Vurgu rengi (paletin rengini değiştirir)'), accentBox),
          h('p', { class: 'muted small' }, 'Bu tercihler yalnızca bu tarayıcıda saklanır. "Sistem" işletim sisteminizin açık/koyu ayarını izler.'))),
      window.APM && window.APM.reports ? window.APM.reports.section() : null,
      h('div', { class: 'grid' },
        h('section', null, h('h3', null, 'Sistem'), h('div', { class: 'panel facts' },
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

  async function viewDenetim() {
    await refreshHeader();
    const list = await api('/api/audit');
    return h('section', null, h('h2', null, 'Denetim günlüğü'),
      table(['Zaman', 'Kullanıcı', 'İşlem', 'Ayrıntı'], list.map((a) => h('tr', null,
        h('td', { class: 'mono small' }, new Date(a.at).toLocaleString('tr-TR')), h('td', null, a.actor),
        h('td', { class: 'mono small' }, a.action), h('td', null, a.detail))), 'Kayıt yok.'));
  }

  // ---- Topoloji haritası ----
  let selectedNode = null;
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


  // Bağımlılık yönünde katmanlar: hiç kimsenin bağımlı olmadığı asset'ler üstte, bağımlılıklar aşağıda.
  function layoutTopology(nodes, edges) {
    const layer = new Map(nodes.map((n) => [n.id, 0]));
    for (let i = 0; i < nodes.length; i++) {
      let changed = false;
      for (const e of edges) {
        const want = layer.get(e.source) + 1;
        if (want > layer.get(e.target) && want < nodes.length) { layer.set(e.target, want); changed = true; }
      }
      if (!changed) break; // döngü varsa katman sayısı sınırlı kalır
    }
    const layers = [];
    for (const n of nodes) { const l = layer.get(n.id); (layers[l] = layers[l] || []).push(n); }
    const rows = layers.filter(Boolean);
    const index = new Map();
    rows.forEach((row, ri) => {
      if (ri > 0) { // çizgi kesişimlerini azaltmak için ebeveynlerin ortalama konumuna göre sırala
        const score = (n) => {
          const ps = edges.filter((e) => e.target === n.id && index.has(e.source)).map((e) => index.get(e.source));
          return ps.length ? ps.reduce((a, b) => a + b, 0) / ps.length : 1e9;
        };
        row.sort((a, b) => score(a) - score(b) || a.name.localeCompare(b.name));
      }
      row.forEach((n, i) => index.set(n.id, i));
    });
    const widest = Math.max(...rows.map((r) => r.length));
    const width = PAD * 2 + widest * NW + (widest - 1) * GX;
    const pos = new Map();
    rows.forEach((row, ri) => {
      const rowW = row.length * NW + (row.length - 1) * GX;
      const x0 = (width - rowW) / 2;
      row.forEach((n, i) => pos.set(n.id, { x: x0 + i * (NW + GX), y: PAD + ri * (NH + GY) }));
    });
    return { pos, width, height: PAD * 2 + rows.length * NH + (rows.length - 1) * GY };
  }

  // ---- Etkileşimli harita: sürükleme, odaklanıp yakınlaşma, kutu içi istatistikler ----
  const POS_KEY = 'apm_topo_pos';
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

    const edgeEls = t.edges.map((e) => { const el = s('path', { class: 'edge' + (e.kind === 'runs_on' ? ' runs' : '') }); svg.append(el); return { e, el }; });
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
        el.setAttribute('class', 'edge' + (e.kind === 'runs_on' ? ' runs' : '') + (hi ? ' hi' : '') + (faded ? ' faded' : '') + (hid ? ' hid' : ''));
        el.setAttribute('marker-end', `url(#${hi ? 'topo-arrow-hi' : 'topo-arrow'})`);
      }
    };
    const place = (id) => { const g = nodeEls.get(id), p = pos.get(id); g.setAttribute('transform', `translate(${p.x.toFixed(1)} ${p.y.toFixed(1)})`); };

    const build = (n) => {
      const sh = shapeOf(n.kind), root = roots.has(n.id), focused = fset && fset.has(n.id);
      const name = n.name.length > 22 ? n.name.slice(0, 21) + '…' : n.name;
      const kids = [s('title', null, `${n.name} (${STATUS[n.status]})`),
        root ? s('rect', { class: 'ring', x: -5, y: -5, width: NW + 10, height: hOf(n.id) + 10, rx: 9 }) : null,
        ...sh.draw(NW, NH),
        s('circle', { class: 'dot', cx: 14 + sh.dx, cy: 17 + sh.dy, r: 4.5 }),
        s('text', { x: 26 + sh.dx, y: 21 + sh.dy }, name),
        s('text', { class: 'sub' + (root ? ' rc' : ''), x: 14 + sh.dx, y: 38 + sh.dy - (n.kind === 'website' ? 2 : 0) }, root ? 'olası kök neden' : n.kind === 'external' ? `dış · ${n.requests} istek${n.failed ? ' · ' + n.failed + ' hata' : ''}` : `${KIND[n.kind] || n.kind}${n.language !== 'none' ? ' · ' + LANG[n.language] : ''}`)];
      if (focused) {
        const lines = statLines(n, (opts.stats || {})[String(n.id)]);
        kids.push(s('rect', { class: 'card', x: 0, y: NH + 4, width: NW, height: CARD_H - 4, rx: 6 }));
        lines.forEach(([k, v], i) => kids.push(s('text', { class: 'st', x: 10, y: NH + 20 + i * 14.5 }, k), s('text', { class: 'sv', x: NW - 10, y: NH + 20 + i * 14.5, 'text-anchor': 'end' }, v)));
      }
      if (n.page_changed) kids.push(s('g', { class: 'chgtag' }, s('rect', { x: NW - 84, y: -9, width: 82, height: 16, rx: 3 }), s('text', { x: NW - 43, y: 3, 'text-anchor': 'middle' }, 'sayfa değişti')));
      if (n.open_alerts) kids.push(s('g', { class: 'badge' }, s('circle', { cx: NW - 4, cy: 2, r: 10 }), s('text', { x: NW - 4, y: 6, 'text-anchor': 'middle' }, n.open_alerts)));
      const faded = fset && !focused;
      const g = s('g', { class: `node ${n.status}${root ? ' root' : ''}${n.id === focus ? ' sel' : ''}${n.kind === 'external' ? ' ext' : ''}${n.page_changed ? ' chg' : ''}${faded ? ' faded' : ''}${shown(n.id) ? '' : ' hid'}`, tabindex: faded || !shown(n.id) ? '-1' : '0', role: 'button',
        'aria-label': `${n.name}, ${STATUS[n.status]}${root ? ', olası kök neden' : ''}`, transform: `translate(${pos.get(n.id).x} ${pos.get(n.id).y})` }, ...kids);
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

  async function topoPanel(t, id) {
    const n = t.nodes.find((x) => x.id === id);
    if (typeof id === 'string') return extPanel(t, n, t.edges.filter((e) => e.target === id));
    const names = new Map(t.nodes.map((x) => [x.id, x.name]));
    const imp = await api(`/api/assets/${id}/impact`);
    const list = (items, empty) => items.length
      ? h('div', { class: 'facts' }, items.map((i) => h('div', { class: 'fact' }, h('span', null, i.name, h('span', { class: 'muted small mono' }, `  ${i.depth}. seviye`)), pill(i.status, STATUS[i.status]))))
      : h('p', { class: 'muted' }, empty);
    const mine = t.edges.filter((e) => e.source === id || e.target === id);
    return h('div', { class: 'grid' },
      h('div', { class: 'panel' }, h('div', { class: 'row' }, h('h3', null, n.name), pill(n.status, STATUS[n.status])),
        h('p', { class: 'muted small' }, `${KIND[n.kind] || n.kind}, ${n.collection === 'agent' ? 'agent' : 'agent\'sız'}${n.open_alerts ? `, ${n.open_alerts} açık alarm` : ''}`),
        h('h3', null, `Bu asset düşerse etkilenenler (${imp.impacted.length})`), list(imp.impacted, 'Ona bağımlı asset yok.')),
      h('div', { class: 'panel' }, h('h3', null, `Bağımlılıkları (${imp.dependencies.length})`), list(imp.dependencies, 'Bağımlı olduğu asset yok.')),
      h('div', { class: 'panel' }, h('h3', null, 'Bağlantılar'),
        mine.length ? h('div', { class: 'facts' }, mine.map((e) => h('div', { class: 'fact' },
          h('span', null, `${names.get(e.source)} → ${names.get(e.target)}`, h('span', { class: 'muted small mono' }, e.kind === 'runs_on' ? '  üzerinde çalışır' : '  bağımlı')),
          e.auto ? h('span', { class: 'muted small' }, 'otomatik') : h('button', { type: 'button', class: 'danger', onclick: async () => { try { await api('/api/topology/edges/' + e.id, { method: 'DELETE' }); render(); } catch (x) { alert(x.message); } } }, 'Sil'))))
          : h('p', { class: 'muted' }, 'Bu asset\'in bağlantısı yok.')));
  }

  async function viewTopoloji() {
    await refreshHeader();
    const [t, stats] = await Promise.all([api('/api/topology'), api('/api/topology/stats').catch(() => ({}))]);
    if (selectedNode && !t.nodes.some((n) => n.id === selectedNode)) selectedNode = null;
    const names = new Map(t.nodes.map((n) => [n.id, n.name]));
    const head = h('section', null, h('h2', null, 'Topoloji'),
      h('p', { class: 'muted' }, 'Oklar "bağımlıdır" yönünü gösterir: ok, bir asset\'ten bağımlı olduğu asset\'e gider. Üstte kullanıcıya yakın servisler, altta altyapı bulunur.'));
    if (!t.nodes.length) {
      return h('div', null, head, h('p', { class: 'panel muted' }, 'Henüz asset yok. Asset\'ler eklendikten sonra aralarındaki bağımlılıkları buradan tanımlayın.'));
    }
    const banner = t.root_causes.length
      ? h('div', { class: 'panel rc-banner' }, h('b', null, 'Olası kök neden: '), t.root_causes.map((id) => names.get(id)).join(', '),
          h('span', { class: 'muted' }, '. Sağlıksız asset\'lerden, bağımlı olduğu hiçbir asset sağlıksız olmayanlar.'))
      : h('div', { class: 'panel muted' }, 'Sağlıksız asset yok; kök neden aranmıyor.');
    const legend = h('div', { class: 'legend' }, ['up', 'down', 'offline', 'unknown'].map((k) => h('span', { class: 'lg ' + k }, STATUS[k])),
      h('span', { class: 'lg rootlg' }, 'Olası kök neden'), h('span', null, 'Kesikli ok: üzerinde çalışır'), h('span', null, '"sayfa değişti": son 24 saatte sayfa yapısı değişti'));
    const shapesLegend = h('div', { class: 'legend shapes' }, ['website', 'webservice', 'service', 'database', 'host', 'network', 'cache', 'queue', 'external']
      .filter((k) => t.nodes.some((n) => n.kind === k) || k !== 'external').map((k) => h('span', { class: 'kl' }, kindIcon(k, 38), KIND[k])));

    const panel = h('section', { id: 'topo-panel' });
    const wrap = h('div', { class: 'topo-wrap' }, drawTopology(t, { focus: selectedNode, stats, height: 560, onSelect: (id) => { selectedNode = selectedNode === id ? null : id; render(); } }));
    const tools = h('div', { class: 'row tools' }, h('span', { class: 'muted small' }, 'Kutuları sürükleyerek yerleştirebilirsiniz (bu tarayıcıda saklanır). Bir kutuya tıklayınca bağlı bileşenlere yakınlaşılır ve istatistikler görünür.'),
      h('span', { class: 'btns' }, selectedNode != null ? h('button', { type: 'button', onclick: () => { selectedNode = null; render(); } }, 'Tümünü göster') : null,
        h('button', { type: 'button', onclick: () => { resetPos(); render(); } }, 'Yerleşimi sıfırla')));
    if (selectedNode) panel.append(await topoPanel(t, selectedNode));
    else panel.append(h('p', { class: 'muted' }, 'Etkisini ve bağımlılıklarını görmek için bir asset\'e tıklayın.'));

    const msg = h('p', { class: 'msg', role: 'status' });
    const opts = t.nodes.filter((n) => n.kind !== 'external').map((n) => [String(n.id), n.name]);
    const form = opts.length < 2 ? h('p', { class: 'muted' }, 'Bağlantı tanımlamak için en az iki asset gerekir.')
      : h('form', { class: 'inline' },
        field('Bağımlı olan (kaynak)', select('e-src', opts)),
        field('Bağımlı olunan (hedef)', select('e-dst', opts, opts[1][0])),
        field('İlişki', select('e-kind', [['depends_on', 'bağımlıdır'], ['runs_on', 'üzerinde çalışır']])),
        h('button', { type: 'submit', class: 'primary' }, 'Bağlantı ekle'));
    if (opts.length >= 2) {
      form.addEventListener('submit', async (e) => {
        e.preventDefault();
        msg.className = 'msg'; msg.textContent = '';
        try {
          await api('/api/topology/edges', { method: 'POST', body: { source: Number($('#e-src', form).value), target: Number($('#e-dst', form).value), kind: $('#e-kind', form).value } });
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
    return h('div', null, head, banner, legend, shapesLegend, tools, wrap, panel,
      h('section', null, h('h3', null, 'Bağlantılar'), h('div', { class: 'panel' }, edgeTable, emsg)),
      h('section', null, h('h3', null, 'Bağlantı tanımla'), h('div', { class: 'panel' }, form, msg)));
  }

  const RENDER = { topoloji: viewTopoloji, genel: viewGenel, assetler: viewAssetler, probelar: viewProbelar, senaryolar: viewSenaryolar, alarmlar: viewAlarmlar, lisans: viewLisans, denetim: viewDenetim, ayarlar: viewAyarlar };
  const AUTO = new Set(['genel', 'assetler', 'topoloji']);

  function current() {
    const v = location.hash.slice(1);
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
    try {
      if (ADMIN_ONLY.includes(v) && ME.role !== 'admin') { $('#view').replaceChildren(h('p', { class: 'error' }, 'Bu sayfa için yönetici yetkisi gerekir (rolünüz: ' + (ROLE_LABEL[ME.role] || ME.role) + ').')); return; }
      const node = await RENDER[v]();
      if (v === current()) $('#view').replaceChildren(node);
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
    key = '';
    try { localStorage.removeItem('apm_key'); } catch (e) { /* yoksay */ }
    ME = { label: '', role: 'admin' }; document.body.removeAttribute('data-role'); $('#role-badge').hidden = true;
    $('#app').hidden = true; $('#login').hidden = false;
  }

  $('#login-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    key = $('#login-key').value.trim();
    const err = $('#login-error');
    err.hidden = true;
    try {
      await api('/api/overview');
      try { localStorage.setItem('apm_key', key); } catch (x) { /* yoksay */ }
      $('#login-key').value = '';
      showApp();
    } catch (ex) { key = ''; err.textContent = 'Anahtar geçersiz veya sunucuya ulaşılamıyor.'; err.hidden = false; }
  });
  $('#logout').addEventListener('click', logout);
  window.addEventListener('hashchange', () => { hideTip(); if (recCleanup) recCleanup(); selectedAsset = null; selectedNode = null; render(); });
  setInterval(() => {
    const typing = document.activeElement && /^(INPUT|SELECT|TEXTAREA)$/.test(document.activeElement.tagName);
    if (key && !$('#app').hidden && AUTO.has(current()) && !typing && !document.querySelector('.editing-row, .node.dragging, .thumb.dragging')) render();
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
