(() => {
  const dialog = document.getElementById('evidence-viewer');
  if (!dialog.showModal) return;
  const evidence = JSON.parse(document.getElementById('evidence-data').textContent);
  const picker = document.getElementById('evidence-file');
  const content = document.getElementById('evidence-content');
  const status = document.getElementById('evidence-status');
  const original = document.getElementById('evidence-original');
  const descriptions = {
    'result.json': 'The recorded outcome, checks, timings and execution details.',
    'task.json': 'The instructions, permitted changes and required test for this run.',
    'events.jsonl': 'The recorded sequence of events, shown in order.',
    'candidate.patch': 'The proposed repair. Added lines are green; removed lines are amber.',
    'seed.patch': 'The change that deliberately planted the bug for this experiment.',
    'context.json': 'The exact prompt, approved repository guidance, permitted tools and run settings. The recorded mode identifies prepared or live use.',
    'pi-session.json': 'The saved Pi conversation and tool replies. Prepared runs use supplied responses; live runs use the model named in the result.',
    'import_verifier_test.go.txt': 'The trusted extra import cases, kept outside the model\'s editable source.',
    'journal.json': 'Intended edits, before/after file fingerprints, observed results and shared limits. Resume compares these records with actual files and check receipts.',
    'handoff.json': 'A short navigation summary rebuilt from observations. Its wording cannot grant permissions or prove that checks passed.',
    'partial.patch': 'The change observed when the run stopped, including edits whose replies may have been lost. This alone does not establish a tested repair.',
    'recovery-stop.json': 'Why a later resume was refused. The earlier session receipt remains historical evidence.',
    'first-entry-result.json': 'The retained result of the first session, before later recovery entries.',
    'second-entry-result.json': 'The retained result of the second session, before any later recovery entry.',
  };
  let activeRun;
  let trigger;

  function displayFile() {
    const name = picker.value;
    const file = activeRun.files[name];
    document.getElementById('evidence-description').textContent = descriptions[name];
    content.replaceChildren();
    original.hidden = file.text === null;
    if (file.text === null) {
      original.removeAttribute('href');
      status.textContent = `${name}: ${file.unavailable}`;
      return;
    }
    original.href = file.href;
    let text = file.text;
    status.textContent = `${name}: embedded copy from this saved run.`;
    if (text.length === 0) {
      status.textContent = `${name}: this file is empty.`;
      return;
    }
    if (name.endsWith('.patch')) {
      const lines = text.split('\n');
      lines.forEach((line, index) => {
        const span = document.createElement('span');
        if (line.startsWith('+') && !line.startsWith('+++')) span.className = 'patch-add';
        if (line.startsWith('-') && !line.startsWith('---')) span.className = 'patch-remove';
        span.textContent = line + (index < lines.length - 1 ? '\n' : '');
        content.append(span);
      });
    } else {
      try {
        text = name.endsWith('.jsonl')
          ? text.trim().split('\n').map(line => JSON.stringify(JSON.parse(line), null, 2)).join('\n\n')
          : JSON.stringify(JSON.parse(text), null, 2);
      } catch {
        status.textContent = `${name}: could not format as JSON; showing the original text.`;
      }
      content.textContent = text;
    }
    content.scrollTop = 0;
  }

  document.addEventListener('click', event => {
    const link = event.target.closest('a[data-evidence]');
    if (!link || event.defaultPrevented || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
    const run = evidence[link.dataset.evidence];
    if (!run) return;
    event.preventDefault();
    activeRun = run;
    trigger = link;
    document.getElementById('evidence-run').textContent = `Run ${run.run_id}`;
    picker.value = 'result.json';
    displayFile();
    dialog.showModal();
    document.body.classList.add('evidence-open');
    picker.focus();
  });

  picker.addEventListener('change', displayFile);
  dialog.addEventListener('keydown', event => {
    if (event.key !== 'Tab') return;
    const controls = [...dialog.querySelectorAll('button, select, a[href], [tabindex="0"]')]
      .filter(element => !element.hidden && !element.disabled);
    const first = controls[0];
    const last = controls[controls.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  });
  document.getElementById('evidence-close').addEventListener('click', () => dialog.close());
  dialog.addEventListener('click', event => {
    const rect = dialog.getBoundingClientRect();
    if (event.target === dialog && (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom)) dialog.close();
  });
  dialog.addEventListener('close', () => {
    document.body.classList.remove('evidence-open');
    trigger?.focus();
  });
})();
