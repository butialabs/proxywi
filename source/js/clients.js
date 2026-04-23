(function () {
  if (typeof window === 'undefined') return;

  window.copyCompose = function (el) {
    el.select();
    function done() {
      var card = el.closest('.card') || el.closest('.modal-content');
      if (!card) return;
      var header = card.querySelector('.card-header strong, .modal-title');
      if (!header) return;
      if (header.dataset.origText === undefined) header.dataset.origText = header.textContent;
      header.textContent = el.dataset.copiedLabel || 'Copied';
      clearTimeout(el._copyTimer);
      el._copyTimer = setTimeout(function () { header.textContent = header.dataset.origText; }, 1500);
    }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(el.value).then(done, function () {
        try { document.execCommand('copy'); done(); } catch (e) {}
      });
    } else {
      try { document.execCommand('copy'); done(); } catch (e) {}
    }
  };

  function readJSON(id) {
    var el = document.getElementById(id);
    if (!el) return null;
    try { return JSON.parse(el.textContent || 'null'); } catch (_) { return null; }
  }

  function fillTpl(tpl, name) {
    return tpl.replace(/\{\{\.Name\}\}/g, name);
  }

  function init() {
    var tr = readJSON('clients-data');
    if (!tr) tr = { titleTpl: '', confirmTpl: '' };

    var editModal = document.getElementById('editClient');
    if (editModal) {
      editModal.addEventListener('show.bs.modal', function (ev) {
        var btn = ev.relatedTarget;
        if (!btn) return;
        var id = btn.getAttribute('data-client-id');
        var name = btn.getAttribute('data-client-name') || '';
        document.getElementById('editClientForm').action = '/clients/' + id + '/edit';
        document.getElementById('editClientName').value = name;
      });
    }

    var composeModal = document.getElementById('viewCompose');
    if (composeModal) {
      composeModal.addEventListener('show.bs.modal', function (ev) {
        var btn = ev.relatedTarget;
        if (!btn) return;
        var id = btn.getAttribute('data-client-id');
        var name = btn.getAttribute('data-client-name') || '';
        document.getElementById('viewComposeTitle').textContent = fillTpl(tr.titleTpl, name);
        var form = document.getElementById('regenerateForm');
        form.action = '/clients/' + id + '/regenerate';
        form.dataset.clientName = name;
        var ta = document.getElementById('viewComposeText');
        ta.value = '';
        fetch('/clients/' + id + '/compose', { credentials: 'same-origin' })
          .then(function (r) { return r.text(); })
          .then(function (t) { ta.value = t; })
          .catch(function () { ta.value = ''; });
      });
    }

    window.confirmRegenerate = function (form) {
      return confirm(fillTpl(tr.confirmTpl, form.dataset.clientName || ''));
    };
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
