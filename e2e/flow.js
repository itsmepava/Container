// End-to-end test of the whole Eimsky lifecycle against a running server:
// factory books → ops locks the plan → forwarders bid (mobile API) → ops awards
// → forwarder uploads its confirmation → factory gets its invoice.
//
//   BASE_URL=http://localhost:8090 node e2e/flow.js
const { chromium } = require('playwright');
const assert = require('assert');

const BASE = process.env.BASE_URL || 'http://localhost:8090';
const SHOTS = process.env.SHOTS_DIR || '';
const pdf = name => ({ name, mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4\n% e2e ' + name + '\n%%EOF\n') });

function step(msg) { console.log('•', msg); }

async function api(path, { method = 'GET', token, body, form } = {}) {
  const headers = {};
  if (token) headers.Authorization = 'Bearer ' + token;
  if (body) headers['Content-Type'] = 'application/json';
  const res = await fetch(BASE + '/api/v1' + path, { method, headers, body: form || (body ? JSON.stringify(body) : undefined) });
  const data = await res.json().catch(() => null);
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${data && data.error}`);
  return data;
}

async function forwarderLogin(phone) {
  const { phone: p, devCode } = await api('/auth/otp', { method: 'POST', body: { phone } });
  assert(devCode, 'server must run with DEV_MODE=true for the e2e test');
  return (await api('/auth/verify', { method: 'POST', body: { phone: p, code: devCode } })).token;
}

async function signIn(browser, email) {
  const ctx = await browser.newContext({ viewport: { width: 1360, height: 900 }, acceptDownloads: true });
  const page = await ctx.newPage();
  page.on('dialog', d => d.accept());
  page.on('pageerror', e => { throw e; });
  await page.goto(BASE + '/login');
  await page.fill('input[name=email]', email);
  await page.fill('input[name=password]', 'eimsky-demo');
  await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);
  return page;
}

async function flashOK(page, text) {
  const flash = page.locator('.flash .alert');
  await flash.waitFor();
  const got = await flash.textContent();
  assert(got.includes(text), `expected flash "${text}", got "${got}"`);
}

(async () => {
  const browser = await chromium.launch();
  try {
    // 1. Factory books 6.6 CBM.
    step('factory books space');
    const fac = await signIn(browser, 'factory@eimsky.test');
    await fac.goto(BASE + '/f/book');
    const options = await fac.locator('#sailing option').allTextContents();
    const last = options[options.length - 1];
    await fac.selectOption('#sailing', { label: last });
    const row = n => fac.locator('#cargo tbody tr').nth(n);
    const fill = async (n, v) => { for (const [k, x] of Object.entries(v)) await row(n).locator(`[name=${k}]`).fill(String(x)); };
    await fill(0, { hs: '6006.32', pieces: 40, length: 160, width: 30, height: 30, kg: 45 });
    await fac.click('#add-line');
    await fill(1, { hs: '9606.21', pieces: 12, length: 50, width: 40, height: 35, kg: 18 });
    await fac.locator('#quote-area').getByText('Estimated cost: up to USD 330').waitFor();
    await fac.locator('#checks').getByText('All checks passed').waitFor();
    assert.strictEqual((await fac.locator('#book-cbm').textContent()).trim(), '6.6');
    if (SHOTS) await fac.screenshot({ path: SHOTS + '/e2e-book.png', fullPage: true });
    await Promise.all([fac.waitForURL(/\/f\/bookings\/\d+$/), fac.click('button:has-text("Book 6.6")')]);
    await fac.click('button:has-text("Confirm booking")');
    await flashOK(fac, 'Booked 6.6 CBM');
    const bookingURL = fac.url();
    const ref = (await fac.locator('h1.mono').textContent()).trim();
    step('booked ' + ref);

    // Documents.
    for (const kind of ['commercial_invoice', 'import_licence']) {
      await Promise.all([fac.waitForLoadState('load'), fac.locator(`form:has(input[name=kind][value=${kind}]) input[type=file]`).setInputFiles(pdf(kind + '.pdf'))]);
      await flashOK(fac, 'Document uploaded');
    }

    // 2. Ops locks the plan.
    step('ops locks the plan');
    const ops = await signIn(browser, 'ops@eimsky.test');
    await ops.goto(BASE + '/ops/bookings?q=' + encodeURIComponent(ref));
    await ops.click(`a:has-text("${ref}")`);
    await ops.click('a:has-text("Open in load planner")');
    await ops.locator('#finalize').waitFor();
    await Promise.all([ops.waitForURL(/\/ops\/tenders\/\d+$/), ops.click('#finalize button:has-text("Finalize FCL")')]);
    await flashOK(ops, 'FCL finalized');
    const tenderID = Number(ops.url().match(/tenders\/(\d+)/)[1]);
    step('tender ' + tenderID + ' open');

    // 3. Two forwarders bid through the mobile API.
    step('forwarders bid');
    const blue = await forwarderLogin('0770000001');
    const serendib = await forwarderLogin('+94770000002');
    const open = await api('/tenders?scope=open', { token: blue });
    assert(open.some(t => t.id === tenderID), 'tender visible to invited forwarder');
    const all = { ocean: true, origin: true, destination: true, destuff: true };
    await api(`/tenders/${tenderID}/bid`, { method: 'PUT', token: serendib, body: { amount: 1900, includes: all } });
    const t = await api(`/tenders/${tenderID}/bid`, { method: 'PUT', token: blue, body: { amount: 1850, includes: all, note: 'e2e' } });
    assert.strictEqual(t.myBid.amount, 1850);

    // 4. Ops closes bidding and awards the auto pick.
    step('ops awards');
    await ops.reload();
    await ops.click('button:has-text("Close bidding now")');
    await flashOK(ops, 'Bidding closed');
    await ops.getByText('Lowest compliant · auto pick').waitFor();
    if (SHOTS) await ops.screenshot({ path: SHOTS + '/e2e-tender.png', fullPage: true });
    await ops.click('button:has-text("Award to Blue Harbour Logistics")');
    await flashOK(ops, 'Awarded');

    // Losing forwarder learns the result but can't open the award.
    const lost = await api(`/tenders/${tenderID}`, { token: serendib });
    assert.strictEqual(lost.result, 'lost');
    await assert.rejects(api(`/tenders/${tenderID}/award`, { token: serendib }));

    // 5. Winner sees the shippers and uploads the booking confirmation.
    step('forwarder uploads confirmation');
    const award = await api(`/tenders/${tenderID}/award`, { token: blue });
    assert(award.shippers.some(s => s.bookingRef === ref), 'winner sees our booking');
    const form = new FormData();
    form.append('file', new Blob([pdf('conf').buffer], { type: 'application/pdf' }), 'booking-confirmation.pdf');
    await api(`/tenders/${tenderID}/confirmation`, { method: 'POST', token: blue, form });

    // 6. Factory sees its final cost and, once the job runs, its invoice.
    step('factory gets its invoice');
    let invoiced = false;
    for (let i = 0; i < 30 && !invoiced; i++) {
      await fac.goto(bookingURL);
      invoiced = await fac.locator('a:has-text("Invoice INV-")').count() > 0;
      if (!invoiced) await fac.waitForTimeout(1000);
    }
    assert(invoiced, 'invoice issued within 30 s (run the server with JOBS_INTERVAL=2s)');
    const cost = await fac.locator('.big').first().textContent();
    assert(/USD \d/.test(cost), 'final cost shown: ' + cost);
    const href = await fac.locator('a:has-text("Invoice INV-")').getAttribute('href');
    const res = await fac.request.get(BASE + href);
    assert.strictEqual(res.status(), 200);
    assert((await res.body()).toString('latin1').startsWith('%PDF'));
    if (SHOTS) await fac.screenshot({ path: SHOTS + '/e2e-booking.png', fullPage: true });
    step('final cost ' + cost.trim() + ' · invoice downloaded');
    console.log('E2E PASSED');
  } finally {
    await browser.close();
  }
})().catch(e => {
  console.error('E2E FAILED:', e.message);
  process.exit(1);
});
