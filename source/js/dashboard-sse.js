(function () {
  var el = document.getElementById('dash-online-count');
  if (!el) return;
  var es = new EventSource('/events/dashboard');
  es.addEventListener('client_online', function () {
    var counter = document.getElementById('dash-online-count');
    if (counter) counter.textContent = parseInt(counter.textContent || '0') + 1;
  });
  es.addEventListener('client_offline', function () {
    var counter = document.getElementById('dash-online-count');
    if (counter) counter.textContent = Math.max(0, parseInt(counter.textContent || '0') - 1);
  });
})();
