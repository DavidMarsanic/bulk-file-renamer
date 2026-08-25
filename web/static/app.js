(function () {
  'use strict';

  var state = {
    folder: '',
    files: [],           // full listing from /api/list: [{path,name,size,modTime}]
    selected: {},         // path -> true, for files currently checked
    recursive: false,
    rules: [],            // [{id, type, ...type-specific fields}]
    previewResults: [],   // last /api/preview results
    collisionCount: 0,
    jobId: null,
    eventSource: null,
    lastBatchId: null,
  };

  var el = function (id) { return document.getElementById(id); };

  // ---- rule definitions ------------------------------------------------

  var RULE_TYPES = [
    { type: 'find-replace', label: 'Find & replace' },
    { type: 'regex-replace', label: 'Regex replace' },
    { type: 'sequence', label: 'Sequence number' },
    { type: 'date', label: 'Date' },
    { type: 'case', label: 'Change case' },
    { type: 'trim', label: 'Trim characters' },
    { type: 'remove-chars', label: 'Remove characters' },
    { type: 'extension', label: 'Change extension' },
  ];
  var RULE_LABELS = {};
  RULE_TYPES.forEach(function (rt) { RULE_LABELS[rt.type] = rt.label; });

  var ruleIdCounter = 0;
  function nextRuleId() { ruleIdCounter += 1; return 'r' + ruleIdCounter; }

  function defaultRule(type) {
    var base = { id: nextRuleId(), type: type };
    switch (type) {
      case 'find-replace': return assign(base, { find: '', replace: '', caseSensitive: false });
      case 'regex-replace': return assign(base, { find: '', replace: '' });
      case 'sequence': return assign(base, { position: 'suffix', start: 1, padding: 2, step: 1, separator: '_' });
      case 'date': return assign(base, { position: 'prefix', dateSource: 'modified', dateFormat: '2006-01-02', separator: '_' });
      case 'case': return assign(base, { caseMode: 'lower' });
      case 'trim': return assign(base, { chars: '' });
      case 'remove-chars': return assign(base, { chars: '' });
      case 'extension': return assign(base, { newExtension: '' });
      default: return base;
    }
  }

  // Small Object.assign-alike so this stays readable without relying on
  // ES6 object spread syntax.
  function assign(target, src) {
    for (var k in src) { if (Object.prototype.hasOwnProperty.call(src, k)) target[k] = src[k]; }
    return target;
  }

  // ---- networking --------------------------------------------------------

  function postJSON(url, body) {
    return fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body || {}),
    }).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (data) {
        if (!res.ok) throw new Error(data.error || 'request failed');
        return data;
      });
    });
  }

  function fetchJSON(url) {
    return fetch(url).then(function (res) {
      if (!res.ok) throw new Error('request failed (' + res.status + ')');
      return res.json();
    });
  }

  // ---- app-mode window auto-resize --------------------------------------

  // Chrome's --app=<url> mode opens a real, single, chrome-less window
  // (no tabs, no address bar) — script-driven resizeTo() is honored on
  // that kind of window. So instead of a fixed size, the window tracks
  // the content's real height as sections (file list, rules, preview)
  // appear, capped so it never grows past the visible screen.
  function setupAutoResize() {
    if (typeof window.resizeTo !== 'function' || typeof ResizeObserver === 'undefined') return;

    var targetInnerWidth = 820;
    var lastHeight = 0;

    function fit() {
      var contentHeight = Math.ceil(document.documentElement.getBoundingClientRect().height);
      if (Math.abs(contentHeight - lastHeight) < 2) return;
      lastHeight = contentHeight;

      var chromeH = Math.max(0, window.outerHeight - window.innerHeight);
      var chromeW = Math.max(0, window.outerWidth - window.innerWidth);
      var maxH = (window.screen.availHeight || 1000) - 60;

      var h = Math.min(maxH, contentHeight + chromeH);
      var w = targetInnerWidth + chromeW;
      try { window.resizeTo(w, h); } catch (e) { /* not resizable in this context; fine */ }
    }

    new ResizeObserver(fit).observe(document.documentElement);
    fit();
  }

  // ---- folder choice ------------------------------------------------------

  el('chooseFolderBtn').addEventListener('click', chooseFolder);
  el('rechooseBtn').addEventListener('click', chooseFolder);

  function chooseFolder() {
    hideError();
    var btn = el('chooseFolderBtn');
    var originalLabel = btn.textContent;
    btn.disabled = true;
    // The native folder-picker can take a moment to appear (and could open
    // behind this window) — without a visible state change here a click
    // looks like it did nothing at all.
    btn.textContent = 'Waiting for folder selection…';

    postJSON('/api/choose-folder', {}).then(function (data) {
      if (!data.path) return; // user canceled
      state.folder = data.path;
      state.rules = [];
      state.previewResults = [];
      state.collisionCount = 0;
      state.lastBatchId = null;
      hideResultBanner();
      el('folderPath').textContent = state.folder;
      el('folderPath').title = state.folder;
      el('chooser').classList.add('hidden');
      el('workspace').classList.remove('hidden');
      el('recursiveToggle').checked = false;
      state.recursive = false;
      renderRules();
      loadFileList();
    }).catch(function (err) {
      showError(err.message);
    }).finally(function () {
      btn.disabled = false;
      btn.textContent = originalLabel;
    });
  }

  // ---- file list -----------------------------------------------------

  el('recursiveToggle').addEventListener('change', function () {
    state.recursive = el('recursiveToggle').checked;
    loadFileList();
  });
  el('selectAllBtn').addEventListener('click', function () {
    state.files.forEach(function (f) { state.selected[f.path] = true; });
    renderFileList();
    schedulePreview();
  });
  el('selectNoneBtn').addEventListener('click', function () {
    state.selected = {};
    renderFileList();
    schedulePreview();
  });

  function loadFileList() {
    hideError();
    postJSON('/api/list', { folder: state.folder, recursive: state.recursive }).then(function (data) {
      state.files = data.files || [];
      state.selected = {};
      state.files.forEach(function (f) { state.selected[f.path] = true; }); // default: all checked
      renderFileList();
      schedulePreview();
    }).catch(function (err) {
      showError(err.message);
    });
  }

  function renderFileList() {
    var container = el('fileList');
    container.innerHTML = '';
    state.files.forEach(function (f) {
      var row = document.createElement('label');
      row.className = 'file-row';

      var checkbox = document.createElement('input');
      checkbox.type = 'checkbox';
      checkbox.checked = !!state.selected[f.path];
      checkbox.addEventListener('change', function () {
        if (checkbox.checked) state.selected[f.path] = true; else delete state.selected[f.path];
        updateFilesSummary();
        schedulePreview();
      });

      var name = document.createElement('span');
      name.className = 'file-name';
      name.textContent = f.name;
      name.title = f.path;

      var size = document.createElement('span');
      size.className = 'file-size';
      size.textContent = formatBytes(f.size);

      row.appendChild(checkbox);
      row.appendChild(name);
      row.appendChild(size);
      container.appendChild(row);
    });
    updateFilesSummary();
  }

  function selectedPaths() {
    return Object.keys(state.selected).filter(function (p) { return state.selected[p]; });
  }

  function updateFilesSummary() {
    var total = state.files.length;
    var selected = selectedPaths();
    var bytes = 0;
    selected.forEach(function (p) {
      var f = state.files.filter(function (x) { return x.path === p; })[0];
      if (f) bytes += f.size;
    });
    el('filesSummary').textContent = total === 0
      ? 'No files found in this folder.'
      : (selected.length + ' of ' + total + ' selected · ' + formatBytes(bytes));
  }

  function formatBytes(n) {
    if (n < 1024) return n + ' B';
    var units = ['KB', 'MB', 'GB', 'TB'];
    var v = n / 1024;
    var i = 0;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return v.toFixed(v < 10 ? 1 : 0) + ' ' + units[i];
  }

  // ---- rule builder -----------------------------------------------------

  el('addRuleBtn').addEventListener('click', function () {
    addRule(el('ruleTypeSelect').value);
  });

  function addRule(type) {
    state.rules.push(defaultRule(type));
    renderRules();
    schedulePreview();
  }
  function removeRule(index) {
    state.rules.splice(index, 1);
    renderRules();
    schedulePreview();
  }
  function moveRule(index, delta) {
    var target = index + delta;
    if (target < 0 || target >= state.rules.length) return;
    var tmp = state.rules[index];
    state.rules[index] = state.rules[target];
    state.rules[target] = tmp;
    renderRules();
    schedulePreview();
  }

  function renderRules() {
    var list = el('ruleList');
    list.innerHTML = '';
    state.rules.forEach(function (rule, i) {
      list.appendChild(renderRuleCard(rule, i, state.rules.length));
    });
    el('noRules').classList.toggle('hidden', state.rules.length > 0);
  }

  function renderRuleCard(rule, index, total) {
    var card = document.createElement('div');
    card.className = 'rule-card';

    var header = document.createElement('div');
    header.className = 'rule-card-header';

    var title = document.createElement('span');
    title.className = 'rule-card-title';
    title.textContent = RULE_LABELS[rule.type] || rule.type;
    header.appendChild(title);

    var controls = document.createElement('div');
    controls.className = 'rule-card-controls';

    var upBtn = document.createElement('button');
    upBtn.type = 'button'; upBtn.className = 'icon-btn'; upBtn.textContent = '↑'; upBtn.title = 'Move up';
    upBtn.disabled = index === 0;
    upBtn.addEventListener('click', function () { moveRule(index, -1); });

    var downBtn = document.createElement('button');
    downBtn.type = 'button'; downBtn.className = 'icon-btn'; downBtn.textContent = '↓'; downBtn.title = 'Move down';
    downBtn.disabled = index === total - 1;
    downBtn.addEventListener('click', function () { moveRule(index, 1); });

    var removeBtn = document.createElement('button');
    removeBtn.type = 'button'; removeBtn.className = 'icon-btn'; removeBtn.textContent = '✕'; removeBtn.title = 'Remove rule';
    removeBtn.addEventListener('click', function () { removeRule(index); });

    controls.appendChild(upBtn);
    controls.appendChild(downBtn);
    controls.appendChild(removeBtn);
    header.appendChild(controls);
    card.appendChild(header);

    var body = document.createElement('div');
    body.className = 'rule-card-body';
    buildRuleFields(rule, body, schedulePreview);
    card.appendChild(body);

    return card;
  }

  function buildRuleFields(rule, container, onUpdate) {
    function update(patch) {
      assign(rule, patch);
      onUpdate();
    }
    switch (rule.type) {
      case 'find-replace':
        container.appendChild(fieldRow('Find', textInput(rule.find, 'text to find', function (v) { update({ find: v }); })));
        container.appendChild(fieldRow('Replace with', textInput(rule.replace, '', function (v) { update({ replace: v }); })));
        container.appendChild(checkboxField(rule.caseSensitive, 'Case-sensitive', function (v) { update({ caseSensitive: v }); }));
        break;
      case 'regex-replace':
        container.appendChild(fieldRow('Pattern (regex)', textInput(rule.find, 'e.g. (\\d+)', function (v) { update({ find: v }); })));
        container.appendChild(fieldRow('Replace with', textInput(rule.replace, 'e.g. #$1', function (v) { update({ replace: v }); })));
        break;
      case 'sequence':
        container.appendChild(fieldRow('Position', selectInput(rule.position, [{ value: 'prefix', label: 'Prefix' }, { value: 'suffix', label: 'Suffix' }], function (v) { update({ position: v }); })));
        container.appendChild(fieldRow('Start at', numberInput(rule.start, function (v) { update({ start: v }); })));
        container.appendChild(fieldRow('Padding', numberInput(rule.padding, function (v) { update({ padding: v }); }, 1)));
        container.appendChild(fieldRow('Step', numberInput(rule.step, function (v) { update({ step: v }); })));
        container.appendChild(fieldRow('Separator', textInput(rule.separator, '', function (v) { update({ separator: v }); })));
        break;
      case 'date':
        container.appendChild(fieldRow('Position', selectInput(rule.position, [{ value: 'prefix', label: 'Prefix' }, { value: 'suffix', label: 'Suffix' }], function (v) { update({ position: v }); })));
        container.appendChild(fieldRow('Date source', selectInput(rule.dateSource, [{ value: 'modified', label: 'File modified date' }, { value: 'today', label: 'Today' }], function (v) { update({ dateSource: v }); })));
        container.appendChild(fieldRow('Format', textInput(rule.dateFormat, '2006-01-02', function (v) { update({ dateFormat: v }); })));
        container.appendChild(fieldRow('Separator', textInput(rule.separator, '', function (v) { update({ separator: v }); })));
        break;
      case 'case':
        container.appendChild(fieldRow('Change to', selectInput(rule.caseMode, [{ value: 'lower', label: 'lower case' }, { value: 'upper', label: 'UPPER CASE' }, { value: 'title', label: 'Title Case' }], function (v) { update({ caseMode: v }); })));
        break;
      case 'trim':
        container.appendChild(fieldRow('Characters to trim (blank = whitespace)', textInput(rule.chars, '', function (v) { update({ chars: v }); })));
        break;
      case 'remove-chars':
        container.appendChild(fieldRow('Characters to remove', textInput(rule.chars, 'e.g. ()[]', function (v) { update({ chars: v }); })));
        break;
      case 'extension':
        container.appendChild(fieldRow('New extension', textInput(rule.newExtension, 'e.g. jpg', function (v) { update({ newExtension: v }); })));
        break;
    }
  }

  function fieldRow(labelText, inputEl) {
    var row = document.createElement('label');
    row.className = 'rule-field';
    var span = document.createElement('span');
    span.className = 'rule-field-label';
    span.textContent = labelText;
    row.appendChild(span);
    row.appendChild(inputEl);
    return row;
  }

  function textInput(value, placeholder, onChange) {
    var input = document.createElement('input');
    input.type = 'text';
    input.value = value || '';
    if (placeholder) input.placeholder = placeholder;
    input.addEventListener('input', function () { onChange(input.value); });
    return input;
  }

  function numberInput(value, onChange, min) {
    var input = document.createElement('input');
    input.type = 'number';
    input.value = String(value);
    if (min != null) input.min = String(min);
    input.addEventListener('input', function () { onChange(parseInt(input.value, 10) || 0); });
    return input;
  }

  function selectInput(value, options, onChange) {
    var select = document.createElement('select');
    options.forEach(function (opt) {
      var o = document.createElement('option');
      o.value = opt.value;
      o.textContent = opt.label;
      if (opt.value === value) o.selected = true;
      select.appendChild(o);
    });
    select.addEventListener('change', function () { onChange(select.value); });
    return select;
  }

  function checkboxField(checked, labelText, onChange) {
    var wrap = document.createElement('label');
    wrap.className = 'rule-checkbox';
    var input = document.createElement('input');
    input.type = 'checkbox';
    input.checked = !!checked;
    input.addEventListener('change', function () { onChange(input.checked); });
    wrap.appendChild(input);
    wrap.appendChild(document.createTextNode(' ' + labelText));
    return wrap;
  }

  // Strip the client-only `id` field before sending rules to the server.
  function sanitizeRules(rules) {
    return rules.map(function (r) {
      var copy = {};
      for (var k in r) { if (k !== 'id' && Object.prototype.hasOwnProperty.call(r, k)) copy[k] = r[k]; }
      return copy;
    });
  }

  // ---- live preview -------------------------------------------------

  var PREVIEW_DEBOUNCE_MS = 200;
  var previewDebounceTimer = null;
  function schedulePreview() {
    clearTimeout(previewDebounceTimer);
    previewDebounceTimer = setTimeout(runPreview, PREVIEW_DEBOUNCE_MS);
  }

  function runPreview() {
    var files = selectedPaths();
    if (files.length === 0 || state.rules.length === 0) {
      state.previewResults = [];
      state.collisionCount = 0;
      renderPreview();
      updateApplyState();
      return;
    }
    postJSON('/api/preview', { folder: state.folder, files: files, rules: sanitizeRules(state.rules) }).then(function (data) {
      state.previewResults = data.results || [];
      state.collisionCount = data.collisionCount || 0;
      renderPreview();
      updateApplyState();
    }).catch(function (err) {
      showError(err.message);
    });
  }

  function renderPreview() {
    var body = el('previewBody');
    body.innerHTML = '';
    var results = state.previewResults;

    el('noPreview').classList.toggle('hidden', results.length !== 0);
    el('previewTableWrap').classList.toggle('hidden', results.length === 0);
    el('previewCount').textContent = results.length ? (results.length + ' file' + (results.length === 1 ? '' : 's')) : '';

    results.forEach(function (r) {
      var tr = document.createElement('tr');
      if (r.collision) tr.className = 'collision-row';

      var tdOld = document.createElement('td');
      tdOld.className = 'old-name';
      tdOld.textContent = r.oldName;

      var tdArrow = document.createElement('td');
      tdArrow.className = 'arrow';
      tdArrow.textContent = '→';

      var tdNew = document.createElement('td');
      tdNew.className = 'new-name';
      tdNew.textContent = r.newName;

      tr.appendChild(tdOld);
      tr.appendChild(tdArrow);
      tr.appendChild(tdNew);
      body.appendChild(tr);
    });

    var banner = el('collisionBanner');
    if (state.collisionCount > 0) {
      banner.textContent = state.collisionCount + ' naming collision' + (state.collisionCount === 1 ? '' : 's') + ' — resolve before applying';
      banner.classList.remove('hidden');
    } else {
      banner.classList.add('hidden');
    }
  }

  function updateApplyState() {
    if (state.applying) return;
    var btn = el('applyBtn');
    btn.disabled = selectedPaths().length === 0 || state.rules.length === 0 || state.collisionCount > 0 || state.previewResults.length === 0;
  }

  // ---- apply / undo -------------------------------------------------

  el('applyBtn').addEventListener('click', onApply);

  function onApply() {
    hideError();
    hideResultBanner();
    var files = selectedPaths();
    setApplying(true);
    postJSON('/api/apply', { folder: state.folder, files: files, rules: sanitizeRules(state.rules) }).then(function (data) {
      state.jobId = data.jobId;
      subscribeJob(data.jobId);
    }).catch(function (err) {
      setApplying(false);
      showError(err.message);
    });
  }

  // If the SSE connection drops before a terminal event arrives, don't
  // leave the UI frozen on "Renaming…" forever.
  var STALL_TIMEOUT_MS = 20000;

  function subscribeJob(jobId) {
    showApplyProgress();
    var es = new EventSource('/api/jobs/' + jobId + '/events');
    state.eventSource = es;

    var stallTimer = null;
    function resetStallTimer() {
      clearTimeout(stallTimer);
      stallTimer = setTimeout(function () {
        closeJob();
        setApplying(false);
        hideApplyProgress();
        showError('Lost connection to Bulk File Renamer while renaming. Is it still running?');
      }, STALL_TIMEOUT_MS);
    }
    resetStallTimer();

    es.onmessage = function (msg) {
      resetStallTimer();
      var e = JSON.parse(msg.data);
      if (e.message) el('applyProgressLabel').textContent = e.message;
      if (e.stage === 'done') {
        clearTimeout(stallTimer);
        closeJob();
        setApplying(false);
        hideApplyProgress();
        onApplyDone(jobId);
      } else if (e.stage === 'error') {
        clearTimeout(stallTimer);
        closeJob();
        setApplying(false);
        hideApplyProgress();
        showError(e.message || 'Something went wrong while renaming.');
      } else if (e.stage === 'canceled') {
        clearTimeout(stallTimer);
        closeJob();
        setApplying(false);
        hideApplyProgress();
      }
    };
    es.onerror = function () {
      clearTimeout(stallTimer);
      closeJob();
      setApplying(false);
      hideApplyProgress();
      showError('Lost connection to the local server.');
    };
  }

  function closeJob() {
    if (state.eventSource) { state.eventSource.close(); state.eventSource = null; }
    state.jobId = null;
  }

  function onApplyDone(jobId) {
    fetchJSON('/api/apply/' + jobId).then(function (result) {
      state.lastBatchId = result.batchId;
      showResultBanner(result);
      // Refresh the listing so it reflects what's actually on disk now.
      loadFileList();
    }).catch(function (err) {
      showError('Files were renamed, but the result could not be loaded: ' + err.message);
    });
  }

  function showResultBanner(result) {
    var renamed = result.renamed || [];
    var errors = result.errors || [];
    el('resultCount').textContent = renamed.length + ' file' + (renamed.length === 1 ? '' : 's') + ' renamed.';
    el('resultErrors').textContent = errors.length ? (' ' + errors.length + ' error(s) occurred — see browser console for details.') : '';
    if (errors.length) { try { console.error('bulk-file-renamer apply errors:', errors); } catch (e) { /* no console */ } }
    el('undoBtn').disabled = !result.batchId;
    el('resultBanner').classList.remove('hidden');
  }
  function hideResultBanner() {
    el('resultBanner').classList.add('hidden');
  }

  el('undoBtn').addEventListener('click', function () {
    if (!state.lastBatchId) return;
    hideError();
    var btn = el('undoBtn');
    btn.disabled = true;
    postJSON('/api/undo', { batchId: state.lastBatchId }).then(function (data) {
      hideResultBanner();
      state.lastBatchId = null;
      loadFileList();
      if (data.errors && data.errors.length) {
        showError('Undo finished with ' + data.errors.length + ' error(s) — see browser console.');
        try { console.error('bulk-file-renamer undo errors:', data.errors); } catch (e) { /* no console */ }
      }
    }).catch(function (err) {
      showError(err.message);
    }).finally(function () {
      btn.disabled = false;
    });
  });

  el('revealFolderBtn').addEventListener('click', function () {
    if (state.folder) postJSON('/api/reveal', { path: state.folder }).catch(function () {});
  });

  function setApplying(v) {
    state.applying = v;
    el('applyBtn').disabled = v;
    if (!v) updateApplyState();
  }

  function showApplyProgress() { el('applyProgress').classList.remove('hidden'); el('applyProgressLabel').textContent = 'Renaming…'; }
  function hideApplyProgress() { el('applyProgress').classList.add('hidden'); }

  // ---- small UI state helpers -----------------------------------------

  function showError(msg) { el('error').textContent = msg; el('error').classList.remove('hidden'); }
  function hideError() { el('error').classList.add('hidden'); el('error').textContent = ''; }

  // Promise doesn't natively have finally in every environment this code
  // might run under, but Chrome (the only browser this app ever runs
  // inside — see internal/browser) has supported it for years, so it's
  // used directly above without a shim.

  // ---- boot --------------------------------------------------------

  function boot() {
    setupAutoResize();
    renderRules();
  }

  boot();
})();
