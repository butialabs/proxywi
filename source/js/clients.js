(function () {
  if (typeof window === 'undefined') return;

  window.copyCompose = function (el) {
    el.select();
    var write = (navigator.clipboard && navigator.clipboard.writeText)
      ? navigator.clipboard.writeText(el.value)
      : Promise.reject();
    write.catch(function () { try { document.execCommand('copy'); } catch (e) {} });
  };

  function readJSON(id) {
    var el = document.getElementById(id);
    if (!el) return null;
    try { return JSON.parse(el.textContent || 'null'); } catch (_) { return null; }
  }

  function fillTpl(tpl, name) {
    return (tpl || '').replace(/\{\{\.Name\}\}/g, name);
  }

  function init() {
    var tr = readJSON('clients-data') || { confirmTpl: '' };
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
