(function () {
  if (typeof window === 'undefined') return;

  function setCheckedClients(container, ids) {
    if (!container) return;
    var set = {};
    ids.forEach(function (id) { set[String(id)] = true; });
    container.querySelectorAll('input[type="checkbox"][name="client_ids"]').forEach(function (cb) {
      cb.checked = !!set[cb.value];
    });
  }

  function parseIDList(csv) {
    if (!csv) return [];
    return csv.split(',').map(function (s) { return s.trim(); }).filter(Boolean);
  }

  function init() {
    var edit = document.getElementById('editAccess');
    if (edit) {
      edit.addEventListener('show.bs.modal', function (ev) {
        var btn = ev.relatedTarget;
        if (!btn) return;
        var id = btn.getAttribute('data-access-id');
        document.getElementById('editAccessForm').action = '/access/' + id + '/edit';
        document.getElementById('editAccessUsername').value = btn.getAttribute('data-access-username') || '';
        document.getElementById('editAccessPassword').value = '';
        document.getElementById('editAccessCidrs').value = btn.getAttribute('data-access-cidrs') || '';
        var ids = parseIDList(btn.getAttribute('data-access-client-ids') || '');
        setCheckedClients(edit.querySelector('[data-access-clients="edit"]'), ids);
      });
    }

    var fresh = document.getElementById('newAccess');
    if (fresh) {
      fresh.addEventListener('show.bs.modal', function () {
        setCheckedClients(fresh.querySelector('[data-access-clients="new"]'), []);
      });
    }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
