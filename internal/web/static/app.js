// BuddyGet : comportements facultatifs. Sans script, chaque page reste
// utilisable (formulaires classiques).
'use strict';

// Confirmation avant toute action destructrice : <form data-confirm="…">.
document.addEventListener('submit', (event) => {
  const message = event.target.dataset.confirm;
  if (message && !window.confirm(message)) {
    event.preventDefault();
    event.stopImmediatePropagation();
  }
}, true);

// Case « tout sélectionner » de la liste des opérations.
document.addEventListener('change', (event) => {
  const all = event.target.closest('[data-select-all]');
  if (!all) return;
  for (const box of document.querySelectorAll(`input[name="ids"][form="${all.dataset.selectAll}"]`)) {
    box.checked = all.checked;
  }
});
