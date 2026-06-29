(function () {
  if (typeof window === 'undefined') return;
  if (typeof window.Chart === 'undefined') return;

  window.Chart.defaults.color = '#9aa8a1';
  window.Chart.defaults.font.family = 'Roboto, system-ui, -apple-system, Segoe UI, sans-serif';
  window.Chart.defaults.borderColor = 'rgba(255,255,255,.06)';

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
    if (!data) return;

    var canvas = document.getElementById('trafficChart');
    if (!canvas) return;

    var ctx = canvas.getContext('2d');
    new window.Chart(ctx, {
      type: 'line',
      data: {
        labels: data.labels,
        datasets: [
          { label: data.tr.in, data: data.dataIn,
            borderColor: '#56b6e6', backgroundColor: makeGradient(ctx, '#56b6e6'),
            tension: .4, fill: true, pointRadius: 0, pointHoverRadius: 4, borderWidth: 2 },
          { label: data.tr.out, data: data.dataOut,
            borderColor: '#34d399', backgroundColor: makeGradient(ctx, '#34d399'),
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
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
