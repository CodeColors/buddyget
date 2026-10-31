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

// Dictée d'une opération : « courses 23 euros 50 » remplit le montant et le
// libellé. Le bouton n'apparaît que si le navigateur reconnaît la parole ;
// la reconnaissance sur l'appareil est demandée quand elle est proposée.
const Recognition = window.SpeechRecognition || window.webkitSpeechRecognition;
if (Recognition) {
  for (const button of document.querySelectorAll('[data-dictate]')) {
    button.hidden = false;
    button.addEventListener('click', () => {
      const recognition = new Recognition();
      recognition.lang = 'fr-FR';
      if ('processLocally' in recognition) recognition.processLocally = true;
      recognition.addEventListener('result', (event) => fillFromSpeech(event.results[0][0].transcript));
      recognition.addEventListener('end', () => button.setAttribute('aria-pressed', 'false'));
      button.setAttribute('aria-pressed', 'true');
      recognition.start();
    });
  }
}

// fillFromSpeech sépare le montant (« 23 », « 23,50 », « 23 euros 50 ») du
// libellé dicté.
function fillFromSpeech(text) {
  const label = document.getElementById('label');
  const match = text.match(/(\d+)(?:[.,](\d{1,2})|\s*(?:euros?|€)(?:\s*(\d{1,2}))?)?/i);
  if (!match) {
    label.value = text.trim();
    return;
  }
  const cents = (match[2] ?? match[3] ?? '').padEnd(2, '0');
  document.getElementById('amount').value = `${match[1]},${cents}`;
  const rest = (text.slice(0, match.index) + text.slice(match.index + match[0].length)).replace(/\s+/g, ' ').trim();
  if (rest) label.value = rest.charAt(0).toUpperCase() + rest.slice(1);
}

// Pointage d'un geste : balayer une ligne [data-swipe] vers la droite
// envoie son formulaire.
let swipe = null;
document.addEventListener('touchstart', (event) => {
  const row = event.target.closest('[data-swipe]');
  swipe = row ? { row, x: event.touches[0].clientX, y: event.touches[0].clientY } : null;
}, { passive: true });
document.addEventListener('touchend', (event) => {
  if (!swipe) return;
  const touch = event.changedTouches[0];
  if (touch.clientX - swipe.x > 60 && Math.abs(touch.clientY - swipe.y) < 30) {
    swipe.row.querySelector('form')?.requestSubmit();
  }
  swipe = null;
});
