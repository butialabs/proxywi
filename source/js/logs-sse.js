(function () {
  if (window.location.search && window.location.search !== '?') {
    var dotEl = document.getElementById('live-dot');
    if (dotEl) dotEl.style.opacity = '0.25';
    return;
  }
  var tbody = document.getElementById('logs-tbody');
  if (!tbody) return;
  var MAX_ROWS = 200;
  var iconOk = '<svg class="w-4 h-4 mr-1" aria-hidden="true"><use href="/dist/img/icons.svg#check-circle"></use></svg>';
  var iconDenied = '<svg class="w-4 h-4 mr-1" aria-hidden="true"><use href="/dist/img/icons.svg#no-symbol"></use></svg>';
  var iconErr = '<svg class="w-4 h-4 mr-1" aria-hidden="true"><use href="/dist/img/icons.svg#x-circle"></use></svg>';
  var es = new EventSource('/events/logs');
  es.addEventListener('proxy_event', function (e) {
    var d = JSON.parse(e.data).data;
    if (document.querySelector('tr[data-id="' + d.id + '"]')) return;
    var empty = document.getElementById('logs-empty');
    if (empty) empty.remove();
    var tr = document.createElement('tr');
    tr.dataset.id = d.id;
    tr.className = 'border-b border-border hover:bg-accent/5';
    var outcomeIcon = d.outcome === 'ok'
      ? '<span class="inline-flex items-center gap-1 text-accent">' + iconOk + '</span>'
      : (d.outcome === 'denied'
        ? '<span class="inline-flex items-center gap-1 text-amber">' + iconDenied + '</span>'
        : '<span class="inline-flex items-center gap-1 text-danger">' + iconErr + '</span>');
    var ts = new Date(d.ts * 1000).toLocaleTimeString('en-GB', { timeZone: window.PROXYWI_TZ || 'UTC', hour12: false });
    var connectCell = d.connect_ms ? d.connect_ms + ' ms' : '&mdash;';
    tr.innerHTML =
      '<td class="px-3 py-2 text-dim text-xs font-mono">' + ts + '</td>' +
      '<td class="px-3 py-2 text-center">' + outcomeIcon + '</td>' +
      '<td class="px-3 py-2"><span class="inline-flex items-center gap-1 text-dim">' + (d.protocol || '') + '</span></td>' +
      '<td class="px-3 py-2">' + (d.user || '') + '</td>' +
      '<td class="px-3 py-2">' + (d.client_name || '') + '</td>' +
      '<td class="px-3 py-2 truncate max-w-[320px]">' + (d.target ? '<code class="font-mono text-accent-hi">' + d.target + '</code>' : '') + '</td>' +
      '<td class="px-3 py-2 text-right font-mono">' + fmtBytes(d.bytes_in) + '</td>' +
      '<td class="px-3 py-2 text-right font-mono">' + fmtBytes(d.bytes_out) + '</td>' +
      '<td class="px-3 py-2 text-right font-mono text-dim">' + connectCell + '</td>' +
      '<td class="px-3 py-2 text-right font-mono text-dim">' + (d.duration_ms || 0) + ' ms</td>';
    tbody.prepend(tr);
    while (tbody.children.length > MAX_ROWS) tbody.lastElementChild.remove();
  });
  function fmtBytes(b) {
    if (!b) return '0 B';
    var k = 1024, sizes = ['B', 'KB', 'MB', 'GB'];
    var i = Math.floor(Math.log(b) / Math.log(k));
    return (b / Math.pow(k, i)).toFixed(2) + ' ' + sizes[i];
  }
})();
