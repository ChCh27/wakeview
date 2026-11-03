(() => {
  // Opening a search result: unfold the collapsed blocks around the target and bring it into view.
  const target = location.hash && document.getElementById(location.hash.slice(1));
  if (target) {
    for (let el = target; el; el = el.parentElement?.closest('details')) {
      if (el.tagName === 'DETAILS') el.open = true;
    }
    target.scrollIntoView({ block: 'center' });
    target.classList.add('flash');
  }

  const log = document.getElementById('log');
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
  });

  es.addEventListener('reload', () => location.reload());
})();
