(function () {
  if (typeof window === 'undefined') return;

  function ready(fn) {
    if (document.readyState !== 'loading') fn();
    else document.addEventListener('DOMContentLoaded', fn);
  }

  // --- Mobile nav toggle ---
  ready(function () {
    var toggle = document.getElementById('nav-toggle');
    var mobile = document.getElementById('nav-mobile');
    if (toggle && mobile) {
      toggle.addEventListener('click', function () {
        mobile.classList.toggle('hidden');
        mobile.classList.toggle('flex');
      });
    }
  });

  // --- Language selector ---
  ready(function () {
    document.querySelectorAll('.lang-select').forEach(function (sel) {
      sel.addEventListener('change', function () {
        document.cookie = 'lang=' + encodeURIComponent(sel.value) + ';path=/;max-age=31536000';
        location.reload();
      });
    });
  });

  // --- Generic confirm dialogs for delete forms ---
  ready(function () {
    document.body.addEventListener('submit', function (e) {
      var form = e.target.closest('.js-confirm');
      if (!form) return;
      var msg = form.dataset.confirm;
      if (msg && !confirm(msg)) {
        e.preventDefault();
      }
    });
  });

  // --- Copy compose textarea on click ---
  function copyText(el) {
    if (!el) return;
    el.select();
    var label = el.dataset.copiedLabel;
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(el.value).then(function () {
        if (label) {
          var orig = el.placeholder;
          el.placeholder = label;
          setTimeout(function () { el.placeholder = orig || ''; }, 1200);
        }
      }).catch(function () {
        try { document.execCommand('copy'); } catch (e) {}
      });
    } else {
      try { document.execCommand('copy'); } catch (e) {}
    }
  }

  ready(function () {
    document.body.addEventListener('click', function (e) {
      var el = e.target.closest('.compose, .js-copy');
      if (el) copyText(el);
    });
  });

  // --- Clients compose modal ---
  ready(function () {
    var page = document.getElementById('clients-page');
    var modal = document.getElementById('compose-modal');
    if (!page || !modal) return;

    var title = document.getElementById('compose-modal-title');
    var text = document.getElementById('compose-modal-text');
    var regenForm = document.getElementById('compose-modal-regen');
    var closeButtons = modal.querySelectorAll('.js-close-modal');

    function open() {
      modal.classList.remove('hidden');
      modal.classList.add('flex');
    }
    function close() {
      modal.classList.add('hidden');
      modal.classList.remove('flex');
    }

    page.addEventListener('click', function (e) {
      var btn = e.target.closest('.js-view-compose');
      if (!btn) return;
      var id = btn.dataset.id;
      var name = btn.dataset.name;
      title.textContent = name;
      regenForm.action = '/clients/' + id + '/regenerate';
      regenForm.dataset.confirm = (regenForm.dataset.confirmBase || '').replace(/\{\{\.Name\}\}/g, name);
      text.value = '';
      open();
      fetch('/clients/' + id + '/compose', { credentials: 'same-origin' })
        .then(function (r) { return r.text(); })
        .then(function (body) {
          text.value = body;
        }).catch(function () {
          text.value = '';
        });
    });

    closeButtons.forEach(function (b) { b.addEventListener('click', close); });
    modal.addEventListener('click', function (e) {
      if (e.target === modal) close();
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && !modal.classList.contains('hidden')) close();
    });

    // store original confirmation template without name substitution
    regenForm.dataset.confirmBase = regenForm.dataset.confirm;
  });

  // --- Access modals ---
  ready(function () {
    var page = document.getElementById('access-page');
    var modal = document.getElementById('access-modal');
    var proxyModal = document.getElementById('proxy-modal');
    if (!page) return;

    var form = document.getElementById('access-form');
    var modalTitle = document.getElementById('access-modal-title');
    var modalIcon = document.getElementById('access-modal-icon');
    var usernameInput = document.getElementById('access-username');
    var passwordInput = document.getElementById('access-password');
    var passwordLabel = document.getElementById('access-password-label');
    var submitBtn = document.getElementById('access-submit');

    var newBtn = document.getElementById('access-new');

    var i18n = page.dataset;

    function openAccess(isEdit, id, username) {
      modal.classList.remove('hidden');
      modal.classList.add('flex');
      if (isEdit) {
        form.action = '/access/' + id + '/edit';
        modalTitle.textContent = i18n.titleEdit || 'Edit access';
        modalIcon.className = 'bi bi-pencil';
        usernameInput.value = username || '';
        passwordInput.value = '';
        passwordInput.required = false;
        passwordLabel.textContent = i18n.passwordOptional || 'Password (leave blank to keep current)';
        submitBtn.textContent = i18n.save || 'Save';
      } else {
        form.action = '/access/new';
        modalTitle.textContent = i18n.titleNew || 'New access';
        modalIcon.className = 'bi bi-plus-circle';
        usernameInput.value = '';
        passwordInput.value = '';
        passwordInput.required = true;
        passwordLabel.textContent = i18n.password || 'Password';
        submitBtn.textContent = i18n.create || 'Create';
      }
    }
    function closeAccess() {
      modal.classList.add('hidden');
      modal.classList.remove('flex');
    }

    if (newBtn) {
      newBtn.addEventListener('click', function () { openAccess(false); });
    }
    page.addEventListener('click', function (e) {
      var edit = e.target.closest('.js-edit-access');
      if (edit) {
        openAccess(true, edit.dataset.id, edit.dataset.username);
        return;
      }
      var proxy = e.target.closest('.js-show-proxy');
      if (proxy) {
        var host = page.dataset.proxyHost || 'localhost';
        var u = encodeURIComponent(proxy.dataset.username);
        document.getElementById('proxy-https').value = 'https://' + u + ':{password}@' + host + ':8443';
        document.getElementById('proxy-socks').value = 'socks5h://' + u + ':{password}@' + host + ':1080';
        proxyModal.classList.remove('hidden');
        proxyModal.classList.add('flex');
      }
    });

    modal.querySelectorAll('.js-close-modal').forEach(function (b) { b.addEventListener('click', closeAccess); });
    modal.addEventListener('click', function (e) { if (e.target === modal) closeAccess(); });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && !modal.classList.contains('hidden')) closeAccess();
    });

    function closeProxy() {
      proxyModal.classList.add('hidden');
      proxyModal.classList.remove('flex');
    }
    proxyModal.querySelectorAll('.js-close-modal').forEach(function (b) { b.addEventListener('click', closeProxy); });
    proxyModal.addEventListener('click', function (e) { if (e.target === proxyModal) closeProxy(); });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && !proxyModal.classList.contains('hidden')) closeProxy();
    });
  });
})();
