(function () {
  if (typeof window === 'undefined') return;
  if (typeof window.Chart !== 'undefined') {
    window.Chart.defaults.color = '#cbd5e1';
    window.Chart.defaults.font.family = 'system-ui, -apple-system, Segoe UI, Roboto, sans-serif';
    window.Chart.defaults.borderColor = 'rgba(255,255,255,.08)';
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
            borderColor: '#60a5fa', backgroundColor: makeGradient(trafficCtx, '#60a5fa'),
            tension: .4, fill: true, pointRadius: 0, pointHoverRadius: 4, borderWidth: 2 },
          { label: data.tr.out, data: data.dataOut,
            borderColor: '#4ade80', backgroundColor: makeGradient(trafficCtx, '#4ade80'),
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
        datasets: [{ data: [sumIn, sumOut], backgroundColor: ['#60a5fa', '#4ade80'], borderWidth: 0, hoverOffset: 6 }]
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
    function totalActive() {
      return Object.values(perClient).reduce(function (s, c) { return s + (c.active || 0); }, 0);
    }
    function totalOnline() {
      return Object.values(perClient).filter(function (c) { return c.online; }).length;
    }
    var totalIn = sumIn, totalOut = sumOut;

    function updateStats() {
      document.querySelector('[data-live="online"]').textContent = totalOnline() || data.onlineCount;
      document.querySelector('[data-live="online-badge"]').textContent = totalOnline() || data.onlineCount;
      document.querySelector('[data-live="active-conns"]').textContent = totalActive();
      document.querySelector('[data-live="bytes-in"]').textContent = humanBytes(totalIn);
      document.querySelector('[data-live="bytes-out"]').textContent = humanBytes(totalOut);
    }

    function onMetrics(d) {
      if (!perClient[d.client_id]) perClient[d.client_id] = { online: true, active: 0 };
      perClient[d.client_id].active = d.active_conns;
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
    function onOnline(d) { perClient[d.id] = { online: true, active: 0 }; updateStats(); }
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
