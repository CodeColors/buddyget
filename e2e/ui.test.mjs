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
// 44 px minimum, champs de 16 px minimum, thème effectivement appliqué.
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
    await page.getByText('Écart', { exact: true }).waitFor();
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

const SCREENS = [
  ['tableau-de-bord', '/'],
  ['operations', '/operations'],
  ['saisie', '/operations/nouvelle'],
  ['comptes', '/comptes'],
  ['categories', '/categories'],
  ['import', '/import'],
  ['historique-imports', '/import/historique'],
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
