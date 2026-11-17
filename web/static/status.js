(function () {
  var $ = function (id) { return document.getElementById(id); };
  var TXT = { up: 'Çalışıyor', degraded: 'Kısmi aksaklık', down: 'Kesinti', unknown: 'Bilinmiyor' };
  var BANNER = { up: 'Tüm sistemler çalışıyor', degraded: 'Bazı hizmetlerde aksaklık var', down: 'Hizmet kesintisi yaşanıyor' };
  function el(tag, cls, text) { var e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; }
  function cls(v) { return v == null ? '' : v >= 99.9 ? 'up' : v >= 99 ? 'degraded' : 'down'; }
  function render(d) {
    document.title = d.title;
    $('title').textContent = d.title;
    var b = $('overall');
    if (!d.published) { b.className = 'banner'; b.textContent = 'Henüz yayınlanan bir hizmet yok.'; $('items').replaceChildren(); return; }
    b.className = 'banner ' + d.overall; b.textContent = BANNER[d.overall] || '';
    var box = $('items'); box.replaceChildren();
    d.items.forEach(function (it) {
      var card = el('div', 'item'), row = el('div', 'row');
      row.append(el('span', 'name', it.name), el('span', 'state ' + it.status, TXT[it.status] || ''));
      var bars = el('div', 'bars');
      it.days.forEach(function (v, i) {
        var s = el('i', cls(v));
        s.title = v == null ? 'Veri yok' : '%' + v.toFixed(2);
        bars.append(s);
      });
      var meta = el('div', 'meta');
      meta.append(el('span', null, '90 gün önce'), el('span', null, it.uptime_pct == null ? '' : '%' + it.uptime_pct.toFixed(2) + ' erişilebilirlik'), el('span', null, 'Bugün'));
      card.append(row, bars, meta);
      box.append(card);
    });
    $('updated').textContent = 'Son güncelleme: ' + new Date(d.updated).toLocaleString((window.LOCALE || 'tr-TR'));
  }
  function load() {
    var m = location.pathname.match(/^\/status\/([a-z0-9-]+)/);
    fetch('/api/public/status' + (m ? '?t=' + m[1] : '')).then(function (r) { return r.json(); }).then(render).catch(function () { $('overall').textContent = 'Durum alınamadı.'; });
  }
  load(); setInterval(load, 60000);
})();
