// Tema ve vurgu rengi: sayfa çizilmeden önce uygulanır (yanıp sönmeyi önler). Depolama kapalıysa varsayılanlar kullanılır.
(function () {
  try {
    var t = localStorage.getItem('apm_theme'), a = localStorage.getItem('apm_accent'), p = localStorage.getItem('apm_palette');
    if (p && /^[a-z]+$/.test(p) && p !== 'default') document.documentElement.setAttribute('data-palette', p);
    if (t === 'light' || t === 'dark') document.documentElement.setAttribute('data-theme', t);
    if (a && /^[a-z]+$/.test(a) && a !== 'auto' && a !== 'teal') document.documentElement.setAttribute('data-accent', a);
  } catch (e) { /* yoksay */ }
})();
