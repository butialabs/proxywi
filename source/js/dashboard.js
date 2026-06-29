(function () {
  if (typeof window === 'undefined') return;
  if (typeof window.Chart !== 'undefined') {
    window.Chart.defaults.color = '#9aa8a1';
    window.Chart.defaults.font.family = 'Roboto, system-ui, -apple-system, Segoe UI, sans-serif';
    window.Chart.defaults.borderColor = 'rgba(255,255,255,.06)';
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

  function readJSON(id) {
    var el = document.getElementById(id);
    if (!el) return null;
    try { return JSON.parse(el.textContent || 'null'); } catch (_) { return null; }
  }

  function makeGradient(ctx, color) {
    var g = ctx.createLinearGradient(0, 0, 0, ctx.canvas.height);
    g.addColorStop(0, color + 'cc');
    g.addColorStop(1, color + '00');
    return g;
  }

  function init() {
    var data = readJSON('dashboard-data');
    if (!data || typeof window.Chart === 'undefined') return;

    var trafficCanvas = document.getElementById('trafficChart');
    var ratioCanvas = document.getElementById('ratioChart');
    if (!trafficCanvas || !ratioCanvas) return;

    var trafficCtx = trafficCanvas.getContext('2d');
    var traffic = new window.Chart(trafficCtx, {
      type: 'line',
      data: {
        labels: data.labels,
        datasets: [
          { label: data.tr.in, data: data.dataIn,
            borderColor: '#56b6e6', backgroundColor: makeGradient(trafficCtx, '#56b6e6'),
            tension: .4, fill: true, pointRadius: 0, pointHoverRadius: 4, borderWidth: 2 },
          { label: data.tr.out, data: data.dataOut,
            borderColor: '#34d399', backgroundColor: makeGradient(trafficCtx, '#34d399'),
            tension: .4, fill: true, pointRadius: 0, pointHoverRadius: 4, borderWidth: 2 }
        ]
      },
      options: {
        responsive: true, maintainAspectRatio: false, interaction: { mode: 'index', intersect: false },
        plugins: {
          legend: { position: 'bottom', labels: { usePointStyle: true, pointStyle: 'circle', padding: 18 } },
          tooltip: {
            backgroundColor: 'rgba(15,23,42,.95)', borderColor: 'rgba(148,163,184,.2)', borderWidth: 1,
            padding: 10, callbacks: { label: function (c) { return c.dataset.label + ': ' + humanBytes(c.raw); } }
          }
        },
        scales: {
          y: { beginAtZero: true, grid: { color: 'rgba(255,255,255,.06)' },
               ticks: { callback: function (v) { return humanBytes(v); } } },
          x: { grid: { display: false }, ticks: { maxTicksLimit: 10, autoSkip: true } }
        }
      }
    });

    var sumIn = data.dataIn.reduce(function (a, b) { return a + b; }, 0);
    var sumOut = data.dataOut.reduce(function (a, b) { return a + b; }, 0);
    var ratio = new window.Chart(ratioCanvas, {
      type: 'doughnut',
      data: {
        labels: [data.tr.in, data.tr.out],
        datasets: [{ data: [sumIn, sumOut], backgroundColor: ['#56b6e6', '#34d399'], borderWidth: 0, hoverOffset: 6 }]
      },
      options: {
        responsive: true, maintainAspectRatio: false, cutout: '68%',
        plugins: {
          legend: { position: 'bottom', labels: { usePointStyle: true, pointStyle: 'circle', padding: 14 } },
          tooltip: { callbacks: { label: function (c) { return c.label + ': ' + humanBytes(c.raw); } } }
        }
      }
    });

    var statusEl = document.getElementById('live-status');
    var statusText = document.getElementById('live-status-text');
    function setStatus(kind, text) {
      statusEl.className = 'badge p-2 border ' + (kind === 'ok' ? 'bg-success-subtle text-success-emphasis border-success-subtle' :
                                              kind === 'err' ? 'bg-danger-subtle text-danger-emphasis border-danger-subtle' :
                                                                'bg-body-tertiary text-body-secondary');
      statusText.textContent = text;
    }

    var perClient = {};
    (data.online || []).forEach(function (c) {
      perClient[c.id] = { online: true };
    });
    function totalOnline() {
      return Object.values(perClient).filter(function (c) { return c.online; }).length;
    }
    var totalIn = sumIn, totalOut = sumOut;

    function setLive(sel, text) {
      var el = document.querySelector(sel);
      if (el) el.textContent = text;
    }

    function updateStats() {
      setLive('[data-live="online"]', totalOnline() || data.onlineCount);
      setLive('[data-live="bytes-in"]', humanBytes(totalIn));
      setLive('[data-live="bytes-out"]', humanBytes(totalOut));
    }

    function onMetrics(d) {
      if (!perClient[d.client_id]) perClient[d.client_id] = { online: true };
      totalIn += d.bytes_in || 0;
      totalOut += d.bytes_out || 0;
      var last = traffic.data.datasets[0].data.length - 1;
      if (last >= 0) {
        traffic.data.datasets[0].data[last] += d.bytes_in || 0;
        traffic.data.datasets[1].data[last] += d.bytes_out || 0;
        traffic.update('none');
      }
      ratio.data.datasets[0].data = [totalIn, totalOut];
      ratio.update('none');
      updateStats();
    }
    function onOnline(d) { perClient[d.id] = { online: true }; updateStats(); }
    function onOffline(d) { if (perClient[d.id]) perClient[d.id].online = false; updateStats(); }

    var es = new EventSource('/events/dashboard');
    es.onopen = function () { setStatus('ok', data.tr.connected); };
    es.onerror = function () { setStatus('err', data.tr.disconnected); };
    es.addEventListener('metrics', function (e) { try { onMetrics(JSON.parse(e.data)); } catch (_) {} });
    es.addEventListener('client_online', function (e) { try { onOnline(JSON.parse(e.data)); } catch (_) {} });
    es.addEventListener('client_offline', function (e) { try { onOffline(JSON.parse(e.data)); } catch (_) {} });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
