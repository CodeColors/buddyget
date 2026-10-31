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

  const tooSmall = await page.$$eval('button, input:not([type=hidden]), .tabbar a', (els) =>
    els
      .filter((el) => {
        const r = el.getBoundingClientRect();
        return r.height < 44 || r.width < 44;
      })
      .map((el) => el.outerHTML.slice(0, 80)),
  );
  assert.deepEqual(tooSmall, [], `${label} : cibles tactiles < 44 px`);

  const fontSizes = await page.$$eval('input:not([type=hidden])', (els) => els.map((el) => parseFloat(getComputedStyle(el).fontSize)));
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

test('déconnexion', async () => {
  await withPage(360, 'light', async (page) => {
    await page.goto(`${BASE}/`);
    await page.getByRole('button', { name: 'Se déconnecter' }).click();
    await page.waitForURL(`${BASE}/login`);
    await page.goto(`${BASE}/`);
    assert.equal(page.url(), `${BASE}/login`);
  }, session);
});
