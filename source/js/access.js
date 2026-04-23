(function () {
  if (typeof window === 'undefined') return;

  function init() {
    var m = document.getElementById('editAccess');
    if (!m) return;
    m.addEventListener('show.bs.modal', function (ev) {
      var btn = ev.relatedTarget;
      if (!btn) return;
      var id = btn.getAttribute('data-access-id');
      document.getElementById('editAccessForm').action = '/access/' + id + '/edit';
      document.getElementById('editAccessUsername').value = btn.getAttribute('data-access-username') || '';
      document.getElementById('editAccessPassword').value = '';
      document.getElementById('editAccessCidrs').value = btn.getAttribute('data-access-cidrs') || '';
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
