// Parcours et contrôles d'affichage : 360 / 768 / 1280 px, thèmes clair et
// sombre. Le binaire est lancé sur une base vierge ; les captures sont
// écrites dans e2e/screenshots/ (non versionné, publié par la CI).
import { after, before, test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';
import { chromium } from 'playwright';
import AxeBuilder from '@axe-core/playwright';

const BIN = process.env.BUDDYGET_BIN ?? path.resolve(import.meta.dirname, '../bin/buddyget');
const PORT = process.env.E2E_PORT ?? '18090';
const BASE = `http://127.0.0.1:${PORT}`;
const SHOTS = path.join(import.meta.dirname, 'screenshots');
const USER = 'hugo';
const PASSWORD = 'une phrase de passe solide';
const WIDTHS = [360, 768, 1280];
const SCHEMES = ['light', 'dark'];
const BACKGROUND = { light: 'rgb(245, 246, 248)', dark: 'rgb(16, 18, 23)' };
const TARGETS = 'button, input:not([type=hidden]), select, textarea, summary, a.btn, .tabbar a, .fab, .tile, .row-link, .tx-link';

let server;
let dataDir;
let browser;
let session; // état de connexion réutilisé par les tests authentifiés

before(async () => {
  dataDir = await mkdtemp(path.join(tmpdir(), 'buddyget-e2e-'));
  await mkdir(SHOTS, { recursive: true });
  server = spawn(BIN, [], { env: { ...process.env, PORT, DATA_DIR: dataDir }, stdio: 'inherit' });
  await waitHealthy();
  browser = await chromium.launch();
});

after(async () => {
  await browser?.close();
  if (server && server.exitCode === null) {
    server.kill('SIGTERM');
    await once(server, 'exit');
  }
  await rm(dataDir, { recursive: true, force: true });
});

async function waitHealthy() {
  for (let i = 0; i < 50; i++) {
    try {
      if ((await fetch(`${BASE}/healthz`)).ok) return;
    } catch {
      // serveur pas encore prêt
    }
    await sleep(100);
  }
  throw new Error(`BuddyGet ne répond pas sur ${BASE}`);
}

async function withPage(width, colorScheme, fn, storageState) {
  const context = await browser.newContext({ viewport: { width, height: 800 }, colorScheme, locale: 'fr-FR', storageState });
  try {
    await fn(await context.newPage(), context);
  } finally {
    await context.close();
  }
}

// Contrôles communs : pas de défilement horizontal, cibles tactiles de
// 44 px minimum, champs de 16 px minimum, thème effectivement appliqué,
// règles WCAG 2.1 A et AA vérifiées par axe (le moteur de Lighthouse).
async function checkLayout(page, label, scheme) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  assert.equal(overflow, 0, `${label} : défilement horizontal`);

  // Une case à cocher ou un bouton radio se touche par son libellé.
  const tooSmall = await page.$$eval(TARGETS, (els) =>
    els
      .filter((el) => el.getClientRects().length > 0)
      .map((el) => (el.matches('[type=checkbox], [type=radio]') ? el.closest('label') ?? el : el))
      .filter((el) => {
        const r = el.getBoundingClientRect();
        return r.height < 44 || r.width < 44;
      })
      .map((el) => el.outerHTML.slice(0, 80)),
  );
  assert.deepEqual(tooSmall, [], `${label} : cibles tactiles < 44 px`);

  const fontSizes = await page.$$eval('input:not([type=hidden]), select, textarea', (els) =>
    els.filter((el) => el.getClientRects().length > 0).map((el) => parseFloat(getComputedStyle(el).fontSize)),
  );
  assert.ok(fontSizes.every((size) => size >= 16), `${label} : champ < 16 px (${fontSizes})`);

  const background = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  assert.equal(background, BACKGROUND[scheme], `${label} : thème ${scheme} non appliqué`);

  const { violations } = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  assert.deepEqual(
    violations.map((v) => `${v.id} : ${v.nodes.slice(0, 3).map((n) => n.target.join(' ')).join(' | ')}`),
    [],
    `${label} : accessibilité`,
  );
}

async function login(page) {
  await page.getByLabel('Identifiant').fill(USER);
  await page.getByLabel('Mot de passe').fill(PASSWORD);
  await page.getByRole('button', { name: 'Se connecter' }).click();
  await page.waitForURL(`${BASE}/`);
}

test('création du premier compte au premier lancement', async () => {
  await withPage(360, 'light', async (page, context) => {
    await page.goto(`${BASE}/`);
    assert.equal(page.url(), `${BASE}/setup`);
    await checkLayout(page, 'création du compte', 'light');
    await page.getByLabel('Identifiant').fill(USER);
    await page.getByLabel('Mot de passe', { exact: true }).fill(PASSWORD);
    await page.getByLabel('Confirmation du mot de passe').fill(PASSWORD);
    await page.getByRole('button', { name: 'Créer le compte' }).click();
    await page.waitForURL(`${BASE}/`);
    await page.getByRole('heading', { name: `Bonjour ${USER}` }).waitFor();
    session = await context.storageState();
  });
});

for (const scheme of SCHEMES) {
  for (const width of WIDTHS) {
    test(`connexion et tableau de bord à ${width} px, thème ${scheme}`, async () => {
      await withPage(width, scheme, async (page) => {
        await page.goto(`${BASE}/login`);
        await checkLayout(page, `connexion ${width} px ${scheme}`, scheme);
        await page.screenshot({ path: path.join(SHOTS, `connexion-${width}-${scheme}.png`), fullPage: true });

        await login(page);
        await checkLayout(page, `tableau de bord ${width} px ${scheme}`, scheme);
        await page.screenshot({ path: path.join(SHOTS, `tableau-de-bord-${width}-${scheme}.png`), fullPage: true });
      });
    });
  }
}

test('message clair en cas de mauvais mot de passe', async () => {
  await withPage(360, 'light', async (page) => {
    await page.goto(`${BASE}/login`);
    await page.getByLabel('Identifiant').fill(USER);
    await page.getByLabel('Mot de passe').fill('pas le bon mot de passe');
    await page.getByRole('button', { name: 'Se connecter' }).click();
    await page.getByRole('alert').filter({ hasText: 'Identifiant ou mot de passe incorrect.' }).waitFor();
    assert.equal(await page.getByLabel('Identifiant').inputValue(), USER);
  });
});

test('choix manuel du thème mémorisé et appliqué sans flash', async () => {
  await withPage(360, 'light', async (page) => {
    await page.goto(`${BASE}/`);
    await page.getByRole('button', { name: 'Thème sombre' }).click();
    await page.waitForURL(`${BASE}/`);
    await checkLayout(page, 'thème sombre forcé', 'dark');
    assert.equal(await page.getByRole('button', { name: 'Thème sombre' }).getAttribute('aria-pressed'), 'true');

    // Le thème figure dans le HTML initial : aucun script n'intervient.
    const html = await (await page.request.get(`${BASE}/`)).text();
    assert.match(html, /<html lang="fr" data-theme="dark">/);

    await page.getByRole('button', { name: 'Thème automatique' }).click();
    await page.waitForURL(`${BASE}/`);
    await checkLayout(page, 'retour au thème automatique', 'light');
  }, session);
});

test('création des comptes depuis le tableau de bord', async () => {
  await withPage(360, 'light', async (page) => {
    await page.goto(`${BASE}/`);
    await page.getByRole('link', { name: 'Créer un compte' }).click();
    await page.getByLabel('Nom du compte').fill('Compte courant');
    await page.getByLabel('Solde initial').fill('1 500');
    await page.getByRole('button', { name: 'Enregistrer' }).click();
    await page.getByRole('status').filter({ hasText: 'Compte enregistré.' }).waitFor();

    await page.getByRole('link', { name: 'Nouveau compte' }).click();
    await page.getByLabel('Nom du compte').fill('Livret A');
    await page.getByLabel('Type').selectOption('epargne');
    await page.getByLabel('Solde initial').fill('3000');
    await page.getByRole('button', { name: 'Enregistrer' }).click();
    await page.getByRole('status').filter({ hasText: 'Compte enregistré.' }).waitFor();
    await checkLayout(page, 'liste des comptes', 'light');
  }, session);
});

test('saisie au clavier : bouton +, montant, Tab, libellé, Entrée', async () => {
  await withPage(360, 'light', async (page) => {
    await page.goto(`${BASE}/`);
    const start = performance.now();
    await page.getByRole('link', { name: 'Opération : nouvelle saisie' }).click();
    await page.waitForURL(/\/operations\/nouvelle/);
    await page.keyboard.type('12,50'); // le montant a le focus
    await page.keyboard.press('Tab');
    await page.keyboard.type('Boulangerie');
    await page.keyboard.press('Enter');
    await page.getByRole('status').filter({ hasText: 'Opération enregistrée.' }).waitFor();
    const elapsed = performance.now() - start;

    // Retour à la page de départ, solde à jour (1 500 − 12,50).
    assert.equal(page.url(), `${BASE}/`);
    await page.getByText(/1\s487,50/).first().waitFor();
    assert.ok(elapsed < 15_000, `saisie en ${elapsed} ms`);
  }, session);
});

test('virement entre comptes : champs adaptés sans script', async () => {
  await withPage(360, 'dark', async (page) => {
    await page.goto(`${BASE}/operations/nouvelle`);
    const category = page.getByLabel('Catégorie');
    const target = page.getByLabel('Vers le compte');
    assert.ok(await category.isVisible());
    assert.ok(!(await target.isVisible()));
    await page.getByRole('radio', { name: 'Virement' }).check();
    assert.ok(!(await category.isVisible()));
    await page.getByLabel('Montant (€)').fill('200');
    await page.getByLabel('Libellé').fill('Épargne du mois');
    await target.selectOption({ label: 'Livret A' });
    await checkLayout(page, 'formulaire de virement', 'dark');
    await page.screenshot({ path: path.join(SHOTS, 'virement-360-dark.png'), fullPage: true });
    await page.getByRole('button', { name: 'Enregistrer', exact: true }).click();
    await page.getByRole('status').filter({ hasText: 'Virement enregistré.' }).waitFor();
  }, session);
});

test('recherche instantanée et édition par lot', async () => {
  await withPage(360, 'light', async (page) => {
    for (const [label, amount] of [['Café de la Gare', '3,50'], ['Café du Port', '4'], ['Supermarché', '82,10']]) {
      const res = await page.request.post(`${BASE}/operations`, {
        form: { type: 'depense', amount, label, date: '2026-10-20', account: '1', back: '/operations' },
        maxRedirects: 0,
      });
      assert.equal(res.status(), 303);
    }
    await page.goto(`${BASE}/operations`);
    await page.evaluate(() => { window.pageMarker = true; });
    await page.getByRole('searchbox', { name: 'Rechercher' }).fill('cafe');
    await page.getByText('2 opérations').waitFor();
    assert.match(page.url(), /q=cafe/);
    assert.equal(await page.evaluate(() => window.pageMarker), true, 'la recherche ne recharge pas la page');

    await page.getByText('Modifier par lot').click();
    await page.getByLabel('Tous les résultats de la recherche').check();
    await page.locator('#lot-categorie').selectOption({ label: 'Alimentation › Restaurants' });
    page.once('dialog', (dialog) => dialog.accept());
    await page.locator('#lot').getByRole('button', { name: 'Appliquer' }).click();
    await page.getByRole('status').filter({ hasText: '2 opérations modifiées.' }).waitFor();
    assert.equal(await page.locator('.tx-cat').filter({ hasText: 'Alimentation › Restaurants' }).count(), 2);
  }, session);
});

const TESTDATA = path.resolve(import.meta.dirname, '../testdata');

test('import d\'un relevé en trois étapes puis rapprochement', async () => {
  await withPage(360, 'light', async (page) => {
    await page.goto(`${BASE}/import`);
    await page.getByLabel('Compte qui reçoit les opérations').selectOption('1');
    await page.setInputFiles('#file', path.join(TESTDATA, 'sg/releve-octobre.csv'));
    await page.getByRole('button', { name: 'Continuer' }).click();
    await page.getByRole('heading', { name: "Vérifier l'import" }).waitFor();
    await checkLayout(page, 'aperçu de l\'import', 'light');
    await page.screenshot({ path: path.join(SHOTS, 'import-apercu-360-light.png'), fullPage: true });

    // Décocher une ligne met à jour le résumé sans recharger la page.
    await page.evaluate(() => { window.pageMarker = true; });
    await page.getByLabel('Importer la ligne 9').uncheck();
    const commit = page.getByRole('button', { name: 'Importer 5 opérations' });
    await commit.waitFor();
    assert.equal(await page.evaluate(() => window.pageMarker), true);
    await commit.click();

    await page.getByRole('status').filter({ hasText: '5 opérations importées.' }).waitFor();
    await page.getByLabel('Solde (€)').fill('2 000');
    await page.getByRole('button', { name: 'Comparer' }).click();
    await page.getByRole('status').filter({ hasText: 'Solde bancaire enregistré.' }).waitFor();
    await page.getByText('Reste à pointer', { exact: true }).waitFor();
    await checkLayout(page, 'rapprochement', 'light');
    await page.screenshot({ path: path.join(SHOTS, 'rapprochement-360-light.png'), fullPage: true });
  }, session);
});

test('fichier inconnu : choix des colonnes sur mobile', async () => {
  await withPage(360, 'dark', async (page) => {
    await page.goto(`${BASE}/import`);
    await page.setInputFiles('#file', path.join(TESTDATA, 'generique/sans-entete.csv'));
    await page.getByRole('button', { name: 'Continuer' }).click();
    await page.getByRole('heading', { name: 'Colonnes du fichier' }).waitFor();
    await checkLayout(page, 'choix des colonnes', 'dark');
    await page.screenshot({ path: path.join(SHOTS, 'import-colonnes-360-dark.png'), fullPage: true });

    await page.getByLabel('Montant', { exact: true }).selectOption({ label: 'C — -750.00' });
    await page.getByRole('button', { name: 'Continuer' }).click();
    await page.getByRole('button', { name: 'Importer 3 opérations' }).waitFor();
    page.once('dialog', (dialog) => dialog.accept());
    await page.getByRole('button', { name: "Abandonner l'import" }).click();
    await page.getByRole('status').filter({ hasText: 'Import abandonné.' }).waitFor();
  }, session);
});

test('règle de catégorisation en un clic depuis une opération', async () => {
  await withPage(360, 'light', async (page) => {
    await page.goto(`${BASE}/operations?q=boulangerie`);
    await page.locator('.tx-label').getByText('Boulangerie', { exact: true }).click();
    await page.getByRole('heading', { name: 'Catégoriser automatiquement' }).waitFor();
    assert.equal(await page.getByLabel('Si le libellé contient').inputValue(), 'Boulangerie');
    await page.locator('#rule-category').selectOption({ label: 'Alimentation › Courses' });
    await checkLayout(page, 'règle en un clic', 'light');
    await page.getByRole('button', { name: 'Créer la règle' }).click();
    await page.getByRole('status').filter({ hasText: /Règle enregistrée\. \d+ opérations? catégorisées?\./ }).waitFor();
    assert.match(page.url(), /\/operations\?q=boulangerie$/);
    await page.locator('.tx-cat').filter({ hasText: 'Alimentation › Courses' }).first().waitFor();
  }, session);
});

test('virement interne détecté puis relié', async () => {
  await withPage(360, 'dark', async (page) => {
    // Le prélèvement EDF importé (−78,32 €) retrouve son pendant sur le livret.
    const res = await page.request.post(`${BASE}/operations`, {
      form: { type: 'revenu', amount: '78,32', label: 'VIR RECU', date: '2026-10-31', account: '2', back: '/operations' },
      maxRedirects: 0,
    });
    assert.equal(res.status(), 303);
    await page.goto(`${BASE}/operations`);
    await page.getByRole('link', { name: /virements? internes? possibles? à vérifier/ }).click();
    await page.getByRole('heading', { name: 'Virements à rapprocher' }).waitFor();
    await checkLayout(page, 'virements à rapprocher', 'dark');
    await page.screenshot({ path: path.join(SHOTS, 'virements-a-relier-360-dark.png'), fullPage: true });
    const pair = page.locator('.pair').filter({ hasText: 'PRLV SEPA EDF' });
    await pair.getByRole('button', { name: /Relier en virement/ }).click();
    await page.getByRole('status').filter({ hasText: 'Virement relié' }).waitFor();
  }, session);
});

test('récurrence détectée puis validée sur mobile', async () => {
  await withPage(360, 'light', async (page) => {
    // Trois mois d'abonnement, jusqu'à aujourd'hui.
    const today = new Date();
    for (let i = 2; i >= 0; i--) {
      const d = new Date(Date.UTC(today.getFullYear(), today.getMonth() - i, Math.min(today.getDate(), 28)));
      const res = await page.request.post(`${BASE}/operations`, {
        form: { type: 'depense', amount: '9,99', label: 'SPOTIFY P1234', date: d.toISOString().slice(0, 10), account: '1', back: '/operations' },
        maxRedirects: 0,
      });
      assert.equal(res.status(), 303);
    }
    await page.goto(`${BASE}/recurrences`);
    await page.getByRole('link', { name: /séries? détectées?/ }).first().click();
    const card = page.locator('.candidate').filter({ hasText: 'SPOTIFY' });
    await card.waitFor();
    await checkLayout(page, 'récurrences détectées', 'light');
    await page.screenshot({ path: path.join(SHOTS, 'recurrences-detectees-360-light.png'), fullPage: true });
    await card.getByRole('button', { name: /^Valider/ }).click();
    await page.getByRole('status').filter({ hasText: 'Récurrence validée.' }).waitFor();
    await page.goto(`${BASE}/recurrences`);
    await page.getByText('SPOTIFY P1234').waitFor();
  }, session);
});

test('prévisionnel sur mobile : horizon, échéance écartée, seuil', async () => {
  await withPage(360, 'light', async (page) => {
    await page.goto(`${BASE}/`);
    await page.getByRole('navigation', { name: 'Navigation principale' }).getByRole('link', { name: 'Prévisionnel' }).click();
    await page.getByRole('link', { name: '6 mois' }).click();
    await page.waitForURL(/date=/);
    await page.getByRole('img', { name: /Solde jour par jour/ }).waitFor();
    const line = page.locator('.fc-line').filter({ hasText: 'SPOTIFY P1234' }).first();
    await line.getByRole('button', { name: /^Écarter/ }).click();
    await page.getByRole('status').filter({ hasText: 'Échéance écartée du prévisionnel.' }).waitFor();
    assert.equal(await page.locator('.fc-line.is-excluded').count(), 1);

    await page.getByText('Réglages du prévisionnel').click();
    await page.getByLabel("Seuil d'alerte (€)").fill('5000');
    await page.getByRole('button', { name: 'Enregistrer les réglages' }).click();
    await page.getByRole('status').filter({ hasText: 'Réglages du prévisionnel enregistrés.' }).waitFor();
    await page.getByText('Le solde passe sous le seuil').waitFor();
    await checkLayout(page, 'prévisionnel', 'light');
    await page.screenshot({ path: path.join(SHOTS, 'previsionnel-parcours-360-light.png'), fullPage: true });
  }, session);
});

test('épargne sur mobile : compte créé depuis un modèle, vue Épargne', async () => {
  await withPage(360, 'light', async (page) => {
    await page.goto(`${BASE}/`);
    await page.getByRole('navigation', { name: 'Navigation principale' }).getByRole('link', { name: 'Plus' }).click();
    await page.getByRole('link', { name: /^Épargne/ }).click();
    await page.getByRole('navigation', { name: "Ouvrir un compte d'épargne" }).getByRole('link', { name: 'LDDS' }).click();
    await page.getByText("elles n'ont rien d'officiel").waitFor();
    assert.equal(await page.getByLabel('Taux annuel (%)').inputValue(), '1,70');
    await page.getByLabel('Solde initial').fill('1500');
    await page.getByRole('button', { name: 'Enregistrer' }).click();
    await page.getByRole('status').filter({ hasText: 'Compte enregistré.' }).waitFor();

    await page.goto(`${BASE}/epargne`);
    const card = page.locator('.savings-card').filter({ hasText: 'LDDS' });
    await card.getByText('Par quinzaine').waitFor();
    await card.getByText('avant le plafond').waitFor();
    await checkLayout(page, 'épargne', 'light');
    await page.screenshot({ path: path.join(SHOTS, 'epargne-parcours-360-light.png'), fullPage: true });
  }, session);
});

test('enveloppe dépassée puis alerte dans la cloche, sur mobile', async () => {
  await withPage(360, 'light', async (page) => {
    await page.goto(`${BASE}/plus`);
    await page.getByRole('link', { name: /^Budgets/ }).click();
    await page.getByRole('link', { name: 'Créer une enveloppe' }).click();
    await page.getByLabel('Catégorie').selectOption({ label: 'Alimentation' });
    await page.getByLabel('Plafond mensuel (€)').fill('1');
    await page.getByRole('button', { name: 'Enregistrer' }).click();
    await page.getByRole('status').filter({ hasText: 'Enveloppe enregistrée.' }).waitFor();
    await page.locator('.envelope').filter({ hasText: 'Alimentation' }).getByText('Dépassé').waitFor();
    await checkLayout(page, 'budgets', 'light');
    await page.screenshot({ path: path.join(SHOTS, 'budgets-parcours-360-light.png'), fullPage: true });

    // L'examen des alertes suit l'enregistrement, en arrière-plan.
    await page.waitForFunction(async () => (await (await fetch('/alertes')).text()).includes('dépassée'));
    await page.goto(`${BASE}/`);
    await page.getByRole('link', { name: /^Alertes : \d+ à lire/ }).click();
    await page.getByText('Enveloppe « Alimentation » dépassée', { exact: true }).waitFor();
    await checkLayout(page, 'alertes', 'light');
    await page.screenshot({ path: path.join(SHOTS, 'alertes-parcours-360-light.png'), fullPage: true });
  }, session);
});

test('saisie rapide (étiquettes, modèle) puis pointage sur mobile', async () => {
  await withPage(360, 'light', async (page) => {
    await page.goto(`${BASE}/operations/nouvelle`);
    await page.getByLabel('Montant (€)').fill('1,20');
    await page.getByLabel('Libellé').fill('Boulangerie');
    await page.getByLabel('Étiquettes').fill('quotidien');
    await page.getByRole('button', { name: 'Enregistrer', exact: true }).click();
    await page.getByRole('status').filter({ hasText: 'Opération enregistrée.' }).waitFor();
    const entered = page.locator('.tx').filter({ hasText: 'quotidien' });
    await entered.getByText('Boulangerie', { exact: true }).waitFor();

    await entered.locator('.tx-link').click();
    await page.getByRole('button', { name: 'Enregistrer comme modèle' }).click();
    await page.getByRole('status').filter({ hasText: 'Modèle enregistré' }).waitFor();
    await page.goto(`${BASE}/operations/nouvelle`);
    await page.getByRole('button', { name: /^Boulangerie/ }).click();
    await page.getByRole('status').filter({ hasText: 'Opération « Boulangerie » enregistrée.' }).waitFor();

    // Pointage : solde de la banque, puis un geste par opération.
    await page.goto(`${BASE}/comptes/1/rapprochement`);
    await page.getByLabel('Solde (€)').fill('1000');
    await page.getByRole('button', { name: /^(Comparer|Mettre à jour)$/ }).click();
    await page.getByRole('status').filter({ hasText: 'Solde bancaire enregistré.' }).waitFor();
    const before = await page.locator('#ecart dd').nth(1).textContent();
    const first = page.getByRole('button', { name: /^Pointer « / }).first();
    await first.click();
    await page.getByRole('button', { name: /^Dépointer « / }).first().waitFor();
    await page.waitForFunction((old) => document.querySelectorAll('#ecart dd')[1].textContent !== old, before, { timeout: 5000 });
    await checkLayout(page, 'pointage', 'light');
    await page.screenshot({ path: path.join(SHOTS, 'pointage-parcours-360-light.png'), fullPage: true });
  }, session);
});

test('crédit, projet et scénario « et si » sur mobile', async () => {
  await withPage(360, 'light', async (page) => {
    // Crédit : tableau d'amortissement et mensualité générée.
    await page.goto(`${BASE}/plus`);
    await page.getByRole('link', { name: /^Crédits/ }).click();
    await page.getByRole('link', { name: 'Ajouter un crédit' }).click();
    await page.getByLabel('Nom', { exact: true }).fill('Voiture');
    await page.getByLabel('Capital emprunté (€)').fill('12000');
    await page.getByLabel('Taux nominal annuel (%)').fill('4,9');
    await page.getByLabel('Durée (mois)').fill('48');
    await page.getByLabel('Assurance mensuelle (€)').fill('8');
    await page.getByRole('button', { name: 'Enregistrer' }).click();
    await page.getByRole('status').filter({ hasText: 'Crédit enregistré' }).waitFor();
    await page.getByRole('heading', { name: "Tableau d'amortissement" }).waitFor();
    await page.getByText('4,90 %').waitFor();
    await checkLayout(page, 'crédit', 'light');
    await page.screenshot({ path: path.join(SHOTS, 'credit-parcours-360-light.png'), fullPage: true });

    // Projet : cagnotte sur le LDDS, puis versement programmé.
    await page.goto(`${BASE}/plus`);
    await page.getByRole('link', { name: /^Projets/ }).click();
    await page.getByRole('link', { name: 'Créer un projet' }).click();
    await page.getByLabel('Nom', { exact: true }).fill('Vacances');
    await page.getByLabel('Montant visé (€)').fill('1200');
    const target = new Date();
    target.setMonth(target.getMonth() + 8);
    await page.getByLabel('Pour le').fill(target.toISOString().slice(0, 10));
    await page.getByLabel("Compte d'épargne").selectOption({ label: 'LDDS' });
    await page.getByRole('button', { name: 'Enregistrer' }).click();
    await page.getByRole('status').filter({ hasText: 'Projet enregistré.' }).waitFor();
    const card = page.locator('.goal-card').filter({ hasText: 'Vacances' });
    await card.getByLabel(/^Montant pour la cagnotte/).fill('300');
    await card.getByRole('button', { name: 'Affecter' }).click();
    await page.getByRole('status').filter({ hasText: 'Cagnotte mise à jour.' }).waitFor();
    await page.locator('.goal-card').filter({ hasText: 'Vacances' }).getByRole('button', { name: 'Créer le versement' }).click();
    await page.getByRole('status').filter({ hasText: 'Versement programmé créé' }).waitFor();
    await page.locator('.goal-card').getByText('En bonne voie').waitFor();
    await checkLayout(page, 'projets', 'light');
    await page.screenshot({ path: path.join(SHOTS, 'projets-parcours-360-light.png'), fullPage: true });

    // Scénario : nouveau crédit et abonnement arrêté, comparés à la réalité.
    await page.goto(`${BASE}/plus`);
    await page.getByRole('link', { name: /^Scénarios/ }).click();
    await page.getByLabel('Nom', { exact: true }).fill('Achat moto');
    await page.getByRole('button', { name: 'Créer' }).click();
    await page.getByRole('status').filter({ hasText: 'Scénario créé' }).waitFor();
    await page.getByLabel('Modification', { exact: true }).selectOption({ label: 'Nouveau crédit' });
    await page.getByLabel('Libellé ou nom').fill('Moto');
    await page.getByLabel('Capital emprunté (€)').fill('8000');
    await page.getByLabel('Taux nominal (%)').fill('3');
    await page.getByLabel('Durée (mois)').fill('36');
    assert.equal(await page.getByLabel('Récurrence').isVisible(), false);
    await page.getByRole('button', { name: 'Ajouter au scénario' }).click();
    await page.getByRole('status').filter({ hasText: 'Modification ajoutée au scénario.' }).waitFor();
    await page.getByLabel('Modification', { exact: true }).selectOption({ label: "Arrêt d'une récurrence" });
    const rules = page.getByLabel('Récurrence');
    await rules.selectOption(await rules.locator('option', { hasText: 'SPOTIFY' }).first().getAttribute('value'));
    await page.getByRole('button', { name: 'Ajouter au scénario' }).click();
    await page.getByRole('status').filter({ hasText: 'Modification ajoutée au scénario.' }).waitFor();
    await page.getByRole('button', { name: 'Retirer « Crédit Moto »' }).waitFor();
    await page.getByText('Écart cumulé', { exact: true }).waitFor();
    await checkLayout(page, 'scénario', 'light');
    await page.screenshot({ path: path.join(SHOTS, 'scenario-parcours-360-light.png'), fullPage: true });

    await page.getByRole('link', { name: 'Voir le prévisionnel avec ce scénario' }).click();
    await page.getByRole('heading', { name: /avec le scénario « Achat moto »/ }).waitFor();
    await page.locator('.chart-reference').waitFor({ state: 'attached' });
    await checkLayout(page, 'prévisionnel avec scénario', 'light');
    await page.screenshot({ path: path.join(SHOTS, 'previsionnel-scenario-360-light.png'), fullPage: true });
  }, session);
});

test('partage du foyer, export et sauvegarde sur mobile', async () => {
  await withPage(360, 'light', async (page) => {
    // Membres, puis une dépense partagée au prorata des revenus.
    await page.goto(`${BASE}/plus`);
    await page.getByRole('link', { name: /^Partage du foyer/ }).click();
    for (const [name, income] of [['Hugo', '3000'], ['Léa', '2000']]) {
      await page.getByLabel('Prénom').fill(name);
      await page.getByLabel('Revenu mensuel (€)').fill(income);
      await page.getByRole('button', { name: 'Ajouter' }).click();
      await page.getByRole('status').filter({ hasText: 'Membre ajouté.' }).waitFor();
    }
    await page.goto(`${BASE}/operations/nouvelle`);
    await page.getByLabel('Montant (€)').fill('900');
    await page.getByLabel('Libellé').fill('Loyer partagé');
    await page.getByText('Partage du foyer').click();
    await page.getByLabel('Opération partagée entre membres du foyer').check();
    await page.getByLabel('Payée par').selectOption({ label: 'Hugo' });
    await page.getByRole('radio', { name: 'Au prorata' }).check();
    await checkLayout(page, 'saisie partagée', 'light');
    await page.getByRole('button', { name: 'Enregistrer', exact: true }).click();
    await page.getByRole('status').filter({ hasText: 'Opération enregistrée.' }).waitFor();
    await page.goto(`${BASE}/partage`);
    await page.getByText('Léa doit 360,00 € à Hugo').waitFor();
    await checkLayout(page, 'partage', 'light');
    await page.screenshot({ path: path.join(SHOTS, 'partage-parcours-360-light.png'), fullPage: true });

    // Export des opérations : le CSV se télécharge.
    await page.goto(`${BASE}/exports`);
    const [csv] = await Promise.all([
      page.waitForEvent('download'),
      page.getByRole('button', { name: 'Télécharger le CSV' }).click(),
    ]);
    assert.match(csv.suggestedFilename(), /^buddyget-operations-\d{4}-\d{2}-\d{2}\.csv$/);
    await checkLayout(page, 'exports', 'light');

    // Sauvegarde manuelle.
    await page.goto(`${BASE}/sauvegardes`);
    await page.getByRole('button', { name: 'Sauvegarder maintenant' }).click();
    await page.getByRole('status').filter({ hasText: 'Sauvegarde terminée.' }).waitFor();
    await page.getByRole('link', { name: 'Télécharger' }).first().waitFor();
    await checkLayout(page, 'sauvegardes', 'light');
    await page.screenshot({ path: path.join(SHOTS, 'sauvegardes-parcours-360-light.png'), fullPage: true });
  }, session);
});

const SCREENS = [
  ['tableau-de-bord', '/'],
  ['operations', '/operations'],
  ['saisie', '/operations/nouvelle'],
  ['comptes', '/comptes'],
  ['categories', '/categories'],
  ['import', '/import'],
  ['historique-imports', '/import/historique'],
  ['regles', '/regles'],
  ['virements', '/operations/virements'],
  ['previsionnel', '/previsionnel'],
  ['epargne', '/epargne'],
  ['budgets', '/budgets'],
  ['alertes', '/alertes'],
  ['alertes-reglages', '/alertes/reglages'],
  ['abonnements', '/abonnements'],
  ['rapports', '/rapports'],
  ['rapport-annuel', '/rapports?annee=2026'],
  ['etiquettes', '/etiquettes'],
  ['pointage', '/comptes/1/rapprochement'],
  ['compte-epargne', '/comptes/nouveau?modele=livret_a'],
  ['recurrences', '/recurrences'],
  ['recurrence-saisie', '/recurrences/nouvelle'],
  ['credits', '/credits'],
  ['credit', '/credits/1'],
  ['credit-saisie', '/credits/nouveau'],
  ['projets', '/projets'],
  ['projet-saisie', '/projets/nouveau'],
  ['scenarios', '/scenarios'],
  ['scenario', '/scenarios/1'],
  ['previsionnel-scenario', '/previsionnel?scenario=1'],
  ['partage', '/partage'],
  ['partage-membre', '/partage/membres/1'],
  ['exports', '/exports'],
  ['sauvegardes', '/sauvegardes'],
  ['parametres', '/parametres'],
  ['utilisateurs', '/utilisateurs'],
  ['plus', '/plus'],
];

for (const scheme of SCHEMES) {
  for (const width of WIDTHS) {
    test(`écrans du grand livre à ${width} px, thème ${scheme}`, async () => {
      await withPage(width, scheme, async (page) => {
        for (const [name, url] of SCREENS) {
          await page.goto(`${BASE}${url}`);
          await checkLayout(page, `${name} ${width} px ${scheme}`, scheme);
          await page.screenshot({ path: path.join(SHOTS, `${name}-${width}-${scheme}.png`), fullPage: true });
        }
      }, session);
    });
  }
}

test('déconnexion', async () => {
  await withPage(360, 'light', async (page) => {
    await page.goto(`${BASE}/`);
    await page.getByRole('button', { name: 'Se déconnecter' }).click();
    await page.waitForURL(`${BASE}/login`);
    await page.goto(`${BASE}/`);
    assert.equal(page.url(), `${BASE}/login`);
  }, session);
});
