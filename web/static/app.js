(() => {
  const log = document.getElementById('log');

  // Transcript filters: hiding is done by a class on #log, so entries added live follow it too.
  const KEY = 'wakeview.filters';
  let saved = {};
  try { saved = JSON.parse(localStorage.getItem(KEY)) || {}; } catch {}
  for (const box of document.querySelectorAll('[data-filter]')) {
    const name = box.dataset.filter;
    box.checked = saved[name] !== false;
    const apply = () => log?.classList.toggle('hide-' + name, !box.checked);
    apply();
    box.addEventListener('change', () => {
      apply();
      saved[name] = box.checked;
      try { localStorage.setItem(KEY, JSON.stringify(saved)); } catch {}
    });
  }
  for (const btn of document.querySelectorAll('[data-expand]')) {
    btn.addEventListener('click', () => {
      const open = btn.dataset.expand === '1';
      for (const d of log?.querySelectorAll('details.tool, details.thinking') || []) d.open = open;
    });
  }

  // Opening a search result: unfold the collapsed blocks around the target and bring it into view,
  // even when a filter hides that kind of block.
  const target = location.hash && document.getElementById(location.hash.slice(1));
  if (target) {
    for (let el = target; el; el = el.parentElement?.closest('details')) {
      if (el.tagName === 'DETAILS') {
        el.open = true;
        el.classList.add('forced');
      }
    }
    target.scrollIntoView({ block: 'center' });
    target.classList.add('flash');
  }

  // Copy buttons: data-copy names the element whose text goes to the clipboard.
  for (const btn of document.querySelectorAll('[data-copy]')) {
    btn.addEventListener('click', async () => {
      const source = document.getElementById(btn.dataset.copy);
      if (!source) return;
      const label = btn.textContent;
      try {
        await navigator.clipboard.writeText(source.textContent);
        btn.textContent = 'Copied';
      } catch {
        // No clipboard access: select the text so Ctrl+C copies it.
        getSelection().selectAllChildren(source);
        btn.textContent = 'Press Ctrl+C';
      }
      setTimeout(() => { btn.textContent = label; }, 1500);
    });
  }

  // Jump buttons: each shows only when there is at least a screen to travel in its direction.
  const toTop = document.querySelector('[data-jump="top"]');
  const toBottom = document.querySelector('[data-jump="bottom"]');
  const updateJump = () => {
    const below = document.body.scrollHeight - (window.scrollY + window.innerHeight);
    if (toTop) toTop.hidden = window.scrollY < window.innerHeight;
    if (toBottom) toBottom.hidden = below < window.innerHeight;
  };
  toTop?.addEventListener('click', () => window.scrollTo({ top: 0, behavior: 'smooth' }));
  toBottom?.addEventListener('click', () => window.scrollTo({ top: document.body.scrollHeight, behavior: 'smooth' }));
  window.addEventListener('scroll', updateJump, { passive: true });
  window.addEventListener('resize', updateJump);
  updateJump();

  if (!log || !log.dataset.events) return;

  const atBottom = () => window.innerHeight + window.scrollY >= document.body.scrollHeight - 80;
  const es = new EventSource(log.dataset.events);

  es.addEventListener('message', (e) => {
    const u = JSON.parse(e.data);
    const stick = atBottom();
    if (u.append) log.insertAdjacentHTML('beforeend', u.append);
    for (const r of u.results || []) {
      const slot = document.getElementById('result-' + r.id);
      if (!slot) continue;
      if (r.error) slot.closest('details')?.classList.add('failed');
      slot.outerHTML = r.html;
    }
    if (stick) window.scrollTo(0, document.body.scrollHeight);
    updateJump();
  });

  es.addEventListener('reload', () => location.reload());
})();
