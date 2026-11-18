(function () {
  if (!document.getElementById('clients-page')) return;
  var es = new EventSource('/events/dashboard');
  es.addEventListener('client_online', function (e) {
    var d = JSON.parse(e.data).data;
    var el = document.getElementById('client-status-' + d.id);
    if (el) el.innerHTML = '<span class="inline-flex items-center gap-1 text-accent">Online</span>';
  });
  es.addEventListener('client_offline', function (e) {
    var d = JSON.parse(e.data).data;
    var el = document.getElementById('client-status-' + d.id);
    if (el) el.innerHTML = '<span class="inline-flex items-center gap-1 text-dim">Offline</span>';
  });
  es.addEventListener('client_ping', function (e) {
    var d = JSON.parse(e.data).data;
    var el = document.getElementById('client-ping-' + d.id);
    if (!el) return;
    var cls = d.ping_ms > 2000 ? 'text-amber' : 'text-accent';
    el.innerHTML = '<span class="' + cls + '">' + (Number(d.ping_ms) || 0) + ' ms</span>';
  });
})();
