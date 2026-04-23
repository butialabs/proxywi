(function () {
  if (typeof window === 'undefined') return;

  function readJSON(id) {
    var el = document.getElementById(id);
    if (!el) return null;
    try { return JSON.parse(el.textContent || 'null'); } catch (_) { return null; }
  }

  function humanBytes(n) {
    if (n < 1024) return n + ' B';
    var units = ['KB', 'MB', 'GB', 'TB'];
    var f = n / 1024;
    for (var i = 0; i < units.length; i++) {
      if (f < 1024) return f.toFixed(2) + ' ' + units[i];
      f /= 1024;
    }
    return f.toFixed(2) + ' PB';
  }

  function outcomeBadge(o) {
    if (o === 'ok') return '<span class="badge bg-success-subtle text-success-emphasis"><i class="bi bi-check-circle me-1"></i>ok</span>';
    if (o === 'denied') return '<span class="badge bg-warning-subtle text-warning-emphasis"><i class="bi bi-slash-circle me-1"></i>denied</span>';
    return '<span class="badge bg-danger-subtle text-danger-emphasis"><i class="bi bi-x-circle me-1"></i>' + o + '</span>';
  }

  function pad2(n) { return n < 10 ? '0' + n : '' + n; }

  function escapeHTML(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c];
    });
  }

  function init() {
    var data = readJSON('logs-data');
    if (!data) return;
    var tbody = document.getElementById('logs-tbody');
    if (!tbody) return;

    var statusEl = document.getElementById('logs-status');
    var statusText = document.getElementById('logs-status-text');

    function setStatus(kind, text) {
      statusEl.className = 'badge p-2 border ' + (kind === 'ok' ? 'bg-success-subtle text-success-emphasis border-success-subtle' :
                                              kind === 'err' ? 'bg-danger-subtle text-danger-emphasis border-danger-subtle' :
                                                                'bg-body-tertiary text-body-secondary');
      statusText.textContent = text;
    }

    function prepend(ev) {
      // Older pages would shift their window unexpectedly if we prepended.
      if (!data.onFirstPage) return;
      var emptyRow = document.getElementById('logs-empty');
      if (emptyRow) emptyRow.remove();
      var d = ev.data;
      var t = new Date(d.ts * 1000);
      var tr = document.createElement('tr');
      tr.dataset.id = d.id;
      tr.innerHTML =
        '<td class="text-body-secondary small font-monospace">' + pad2(t.getHours()) + ':' + pad2(t.getMinutes()) + ':' + pad2(t.getSeconds()) + '</td>' +
        '<td>' + outcomeBadge(d.outcome) + '</td>' +
        '<td><span class="badge bg-body-secondary text-body-emphasis">' + escapeHTML(d.protocol) + '</span></td>' +
        '<td><code>' + escapeHTML(d.source_ip) + '</code></td>' +
        '<td>' + escapeHTML(d.user) + '</td>' +
        '<td>' + escapeHTML(d.client_name) + '</td>' +
        '<td class="text-truncate" style="max-width:320px;"><code>' + escapeHTML(d.target) + '</code></td>' +
        '<td class="text-end font-monospace">' + humanBytes(d.bytes_in || 0) + '</td>' +
        '<td class="text-end font-monospace">' + humanBytes(d.bytes_out || 0) + '</td>' +
        '<td class="text-end font-monospace text-body-secondary">' + (d.duration_ms || 0) + ' ms</td>';
      tr.style.backgroundColor = 'rgba(74,222,128,.08)';
      tbody.insertBefore(tr, tbody.firstChild);
      setTimeout(function () {
        tr.style.transition = 'background-color 1.2s';
        tr.style.backgroundColor = '';
      }, 50);
      while (tbody.rows.length > data.pageSize) tbody.deleteRow(tbody.rows.length - 1);
    }

    var es = new EventSource('/events/logs');
    es.onopen = function () { setStatus('ok', data.tr.connected); };
    es.onerror = function () { setStatus('err', data.tr.disconnected); };
    es.addEventListener('proxy_event', function (e) {
      try { prepend(JSON.parse(e.data)); } catch (_) {}
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
